package main

import (
	"context"
	"fmt"
	"io"
	"os"
        "os/signal"
        "syscall"
        //"time"
	"path/filepath"
	"runtime"
	"strings"

	//"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/spf13/cobra"
)

// Config holds configurable parameters.
type Config struct {
	DockerImage   string
	HostPort      string // e.g. "1313"
	ContainerPort string // e.g. "1313"
}

// getConfig reads configuration from environment variables (with defaults).
func getConfig() Config {
	dockerImage := os.Getenv("DOCKER_IMAGE")
	if dockerImage == "" {
		dockerImage = "fortinet-hugo:latest"
	}
	hostPort := os.Getenv("HOST_PORT")
	if hostPort == "" {
		hostPort = "1313"
	}
	containerPort := os.Getenv("CONTAINER_PORT")
	if containerPort == "" {
		containerPort = "1313"
	}
	return Config{
		DockerImage:   dockerImage,
		HostPort:      hostPort,
		ContainerPort: containerPort,
	}
}

// adjustPathForDocker converts paths for WSL2 or native Windows environments.
func adjustPathForDocker(path string) string {
	if runtime.GOOS == "windows" {
		// Convert Unix-like paths (/mnt/c/...) to Windows-style (C:\...)
		if strings.HasPrefix(path, "/mnt/") {
			path = strings.ReplaceAll(path, "/mnt/", "")
			path = strings.ReplaceAll(path, "/", "\\")
			path = strings.ToUpper(path[:1]) + ":" + path[1:]
		}
	} else if isWSL2() {
		// Convert Windows-style paths (C:\...) to WSL-compatible paths (/mnt/c/...)
		if len(path) > 1 && path[1] == ':' {
			drive := strings.ToLower(string(path[0]))
			path = fmt.Sprintf("/mnt/%s%s", drive, strings.ReplaceAll(path[2:], "\\", "/"))
		}
	}
	return path
}

// isWSL2 detects if the program is running inside WSL2.
func isWSL2() bool {
	_, isWSL := os.LookupEnv("WSL_INTEROP")
	return isWSL && runtime.GOOS == "linux"
}

// runContainer uses the Docker SDK to create, start, and (if interactive) attach to a container.
func runContainer(ctx context.Context, cli *client.Client, cfg Config, commandArgs []string, mounts []mount.Mount, portBindings nat.PortMap, interactive bool) error {
	containerConfig := &container.Config{
		Image:        cfg.DockerImage,
		Cmd:          commandArgs,
		Tty:          interactive,
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  interactive,
		OpenStdin:    interactive,
                ExposedPorts: nat.PortSet{
                   nat.Port(cfg.ContainerPort + "/tcp"): struct{}{},
                },
	}

	hostConfig := &container.HostConfig{
		Mounts:       mounts,
		PortBindings: portBindings,
	}

	// Create the container.
	created, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return fmt.Errorf("container create error: %w", err)
	}
        containerID := created.ID

        // Capture CTRL+C (SIGINT/SIGTERM) to stop and remove the container
        sigChan := make(chan os.Signal, 1)
        signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)


        go func() {
            <-sigChan // Wait for a termination signal
            fmt.Println("\nReceived shutdown signal, stopping container...")


            // Stop the container gracefully
            timeOut := 10
            stopTimeout := container.StopOptions{Timeout: &timeOut}
            if err := cli.ContainerStop(context.Background(), containerID, stopTimeout); err != nil {
                fmt.Printf("Error stopping container: %v\n", err)
            } 

            // Remove the container
            if err := cli.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true}); err != nil {
                fmt.Printf("Error removing container: %v\n", err)
            }


            os.Exit(0) // Exit the Go program after cleanup
        }()


	// Start the container.
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("container start error: %w", err)
	}

	// If interactive, attach to the container's I/O.
	if interactive {
		attachResp, err := cli.ContainerAttach(ctx, created.ID, container.AttachOptions{
			Stream: true, Stdout: true, Stderr: true, Stdin: true,
		})
		if err != nil {
			return fmt.Errorf("container attach error: %w", err)
		}
		defer attachResp.Close()

		// Copy stdin to container's input.
		go func() {
			_, _ = io.Copy(attachResp.Conn, os.Stdin)
		}()

		// Copy container's output to stdout.
		_, err = io.Copy(os.Stdout, attachResp.Reader)
		if err != nil && err != io.EOF {
			return fmt.Errorf("copy output error: %w", err)
		}
	} else {
		// For non-interactive commands, stream logs.
		logs, err := cli.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
		if err != nil {
			return fmt.Errorf("container logs error: %w", err)
		}
		defer logs.Close()
		_, err = io.Copy(os.Stdout, logs)
		if err != nil && err != io.EOF {
			return fmt.Errorf("copy logs error: %w", err)
		}
	}

	// Wait for the container to exit.
	statusCh, errCh := cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("container wait error: %w", err)
		}
	case <-statusCh:
	}

	return nil
}

// runHugoCommand builds the proper arguments, mounts, and port bindings for a given Hugo command.
func runHugoCommand(mainCmd string) error {
	var cmdArgs []string
	interactive := false

	switch mainCmd {
	case "server", "shell", "build":
		// These commands run interactively.
		cmdArgs = []string{mainCmd, "--disableFastRender", "--poll"}
		interactive = true
	case "generate_toml", "update_scripts", "update_fdevsec":
		cmdArgs = []string{mainCmd}
	default:
		return fmt.Errorf("unknown command: %s", mainCmd)
	}

	// Get current directory and adjust paths.
	currentDir, err := filepath.Abs(".")
	if err != nil {
		return fmt.Errorf("error getting current directory: %w", err)
	}
	userRepoPath := adjustPathForDocker(currentDir)
	centralRepoPath := adjustPathForDocker(filepath.Join(currentDir, "hugo.toml"))

	// Prepare mount bindings.
	var mounts []mount.Mount
	// All commands get the user repo mounted.
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeBind,
		Source: userRepoPath,
		Target: "/home/UserRepo",
	})
	// For interactive commands, also bind the hugo.toml.
	if interactive {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: centralRepoPath,
			Target: "/home/CentralRepo/hugo.toml",
		})
	}

	// Port bindings only for interactive commands (server, shell, build).
	portBindings := nat.PortMap{}
	cfg := getConfig()
	if interactive {
		containerPort := nat.Port(cfg.ContainerPort + "/tcp")
		portBindings[containerPort] = []nat.PortBinding{
			{
				HostIP:   "0.0.0.0",
				HostPort: cfg.HostPort,
			},
		}
	}

        fmt.Printf("Port Bindings: %+v\n", portBindings)
    
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	// Run the container.
	if err := runContainer(ctx, cli, cfg, cmdArgs, mounts, portBindings, interactive); err != nil {
		return fmt.Errorf("failed to run container: %w", err)
	}
	return nil
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "docker_run",
		Short: "Run the Hugo application inside a Docker container",
	}

	// Interactive subcommands.
	for _, sub := range []string{"server", "shell", "build"} {
		// Capture sub in loop.
		subCmd := sub
		rootCmd.AddCommand(&cobra.Command{
			Use:   subCmd,
			Short: fmt.Sprintf("Run Hugo %s command interactively", subCmd),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runHugoCommand(subCmd)
			},
		})
	}

	// Non-interactive subcommands.
	for _, sub := range []string{"generate_toml", "update_scripts", "update_fdevsec"} {
		// Capture sub in loop.
		subCmd := sub
		rootCmd.AddCommand(&cobra.Command{
			Use:   subCmd,
			Short: fmt.Sprintf("Run Hugo %s command", subCmd),
			RunE: func(cmd *cobra.Command, args []string) error {
				return runHugoCommand(subCmd)
			},
		})
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}


package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	//"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

// Config holds configurable parameters.
type Config struct {
	DockerImage   string
	HostPort      string
	ContainerPort string
	WatchDir      string
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
	watchDir := os.Getenv("WATCH_DIR")
	if watchDir == "" {
		abs, err := filepath.Abs(".")
		if err != nil {
			watchDir = "."
		} else {
			watchDir = abs
		}
	}
	return Config{
		DockerImage:   dockerImage,
		HostPort:      hostPort,
		ContainerPort: containerPort,
		WatchDir:      watchDir,
	}
}

// adjustPathForDocker converts paths for WSL2 or native Windows environments.
func adjustPathForDocker(path string) string {
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(path, "/mnt/") {
			path = strings.ReplaceAll(path, "/mnt/", "")
			path = strings.ReplaceAll(path, "/", "\\")
			path = strings.ToUpper(path[:1]) + ":" + path[1:]
		}
	} else if isWSL2() {
		if len(path) > 1 && path[1] == ':' {
			drive := strings.ToLower(string(path[0]))
			path = fmt.Sprintf("/mnt/%s%s", drive, strings.ReplaceAll(path[2:], "\\", "/"))
		}
	}
	return path
}

func isWSL2() bool {
	_, isWSL := os.LookupEnv("WSL_INTEROP")
	return isWSL && runtime.GOOS == "linux"
}

// startContainer creates and starts the Docker container.
// The 'interactive' flag indicates whether we should add the Hugo config mount.
func startContainer(ctx context.Context, cli *client.Client, cfg Config, cmd []string, interactive bool) (string, error) {
	userRepoPath := adjustPathForDocker(cfg.WatchDir)
	// Always mount the project directory.
	mounts := []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: userRepoPath,
			Target: "/home/UserRepo",
		},
	}
	// For interactive commands, also mount the Hugo config file.
	if interactive {
		configPath := filepath.Join(cfg.WatchDir, "hugo.toml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			fmt.Printf("Warning: Hugo config file not found at %s. The container may exit if Hugo requires it.\n", configPath)
		}
		centralRepoPath := adjustPathForDocker(configPath)
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: centralRepoPath,
			Target: "/home/CentralRepo/hugo.toml",
		})
	}

	containerConfig := &container.Config{
		Image: cfg.DockerImage,
		Cmd:   cmd,
		Tty:   true,
		ExposedPorts: nat.PortSet{
			nat.Port(cfg.ContainerPort + "/tcp"): struct{}{},
		},
	}
	hostConfig := &container.HostConfig{
		Mounts: mounts,
		PortBindings: nat.PortMap{
			nat.Port(cfg.ContainerPort + "/tcp"): []nat.PortBinding{
				{
					HostIP:   "0.0.0.0",
					HostPort: cfg.HostPort,
				},
			},
		},
	}
	created, err := cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("container create error: %w", err)
	}
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("container start error: %w", err)
	}
	fmt.Printf("Started container: %s\n", created.ID)
	return created.ID, nil
}

// attachContainer attaches to the container's I/O.
func attachContainer(ctx context.Context, cli *client.Client, containerID string, interactive bool) error {
	var attachOpts container.AttachOptions
	if interactive {
		attachOpts = container.AttachOptions{
			Stream: true, Stdout: true, Stderr: true, Stdin: true,
		}
	} else {
		attachOpts = container.AttachOptions{
			Stream: true, Stdout: true, Stderr: true,
		}
	}
	resp, err := cli.ContainerAttach(ctx, containerID, attachOpts)
	if err != nil {
		return fmt.Errorf("container attach error: %w", err)
	}
	// Stream container output to stdout.
	go func() {
		_, _ = io.Copy(os.Stdout, resp.Reader)
	}()
	if interactive {
		// Forward user input to the container.
		go func() {
			_, _ = io.Copy(resp.Conn, os.Stdin)
		}()
	}
	return nil
}

// stopAndRemoveContainer stops and removes the specified container.
func stopAndRemoveContainer(cli *client.Client, containerID string) {
	fmt.Println("Stopping container:", containerID)
        timeOut := 10
	stopOpts := container.StopOptions{Timeout: &timeOut}
	if err := cli.ContainerStop(context.Background(), containerID, stopOpts); err != nil {
		fmt.Printf("Error stopping container %s: %v\n", containerID, err)
	}
	if err := cli.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true}); err != nil {
		fmt.Printf("Error removing container %s: %v\n", containerID, err)
	}
}

func watchAndRestart(ctx context.Context, cli *client.Client, cfg Config, containerID *string, cmd []string, interactive bool) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Error creating file watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	if err := watcher.Add(cfg.WatchDir); err != nil {
		fmt.Printf("Error watching directory: %v\n", err)
		return
	}
	fmt.Println("Watching for file changes in:", cfg.WatchDir)

        filepath.Walk(cfg.WatchDir, func(path string, info os.FileInfo, err error) error {
          if err != nil {
            return err
          }
          if info.IsDir() {
            if err := watcher.Add(path); err != nil {
              fmt.Printf("Error watching directory %s: %v\n", path, err)
            }
          }
          return nil
        })

	// Set debounce duration (e.g., 2 seconds)
	debounceDuration := 2 * time.Second
	var debounceTimer *time.Timer

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				fmt.Println("File change detected:", event.Name)
				// Reset the debounce timer on each event
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(debounceDuration)
			}
		case <-func() <-chan time.Time {
			if debounceTimer != nil {
				return debounceTimer.C
			}
			// If no timer is active, return a nil channel (blocks forever)
			return make(chan time.Time)
		}():
			// Timer fired: no further events for debounceDuration, so restart container.
			fmt.Println("Restarting container due to file changes")
			stopAndRemoveContainer(cli, *containerID)
			newID, err := startContainer(ctx, cli, cfg, cmd, interactive)
			if err != nil {
				fmt.Printf("Error restarting container: %v\n", err)
			} else {
				*containerID = newID
				if interactive {
					if err := attachContainer(ctx, cli, newID, interactive); err != nil {
						fmt.Printf("Error attaching to container: %v\n", err)
					}
				}
			}
			// Clear the timer so we don’t accidentally restart again
			debounceTimer = nil
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("Watcher error: %v\n", err)
		case <-ctx.Done():
			return
		}
	}
}

// runServer is the Cobra command for the "server" subcommand.
func runServer(cmd *cobra.Command, args []string) error {
	cfg := getConfig()
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	// Hugo server command: ensure it binds to 0.0.0.0.
	serverCmd := []string{"server", "--bind", "0.0.0.0", "--disableFastRender", "--poll", "--liveReload"}
	containerID, err := startContainer(ctx, cli, cfg, serverCmd, true)
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	if err := attachContainer(ctx, cli, containerID, true); err != nil {
		return fmt.Errorf("failed to attach container: %w", err)
	}
	// Handle CTRL+C to clean up.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived shutdown signal.")
		stopAndRemoveContainer(cli, containerID)
		os.Exit(0)
	}()
	// Start file watcher to restart the container on file changes.
	watchAndRestart(ctx, cli, cfg, &containerID, serverCmd, true)
	return nil
}

// runNonInteractive runs the container once for non-interactive subcommands.
func runNonInteractive(cmd *cobra.Command, args []string) error {
	cfg := getConfig()
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}
	commandArgs := []string{cmd.Name()}
	containerID, err := startContainer(ctx, cli, cfg, commandArgs, false)
	if err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}
	if err := attachContainer(ctx, cli, containerID, false); err != nil {
		return fmt.Errorf("failed to attach container: %w", err)
	}
	statusCh, errCh := cli.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("container wait error: %w", err)
		}
	case <-statusCh:
	}
	return nil
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "docker_run",
		Short: "Run the Hugo application inside a Docker container",
	}

	// Server command with live file watching.
	serverCmd := &cobra.Command{
		Use:   "server",
		Short: "Run Hugo server with live file watching",
		RunE:  runServer,
	}
	rootCmd.AddCommand(serverCmd)

	// Other non-interactive subcommands.
	for _, sub := range []string{"shell", "build", "generate_toml", "update_scripts", "update_fdevsec"} {
		subCmd := &cobra.Command{
			Use:   sub,
			Short: fmt.Sprintf("Run Hugo %s command", sub),
			RunE:  runNonInteractive,
		}
		rootCmd.AddCommand(subCmd)
	}

	if err := rootCmd.Execute(); err != nil {
		fmt.Println("Error:", err)
		os.Exit(1)
	}
}


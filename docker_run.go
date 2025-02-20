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

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/fsnotify/fsnotify"
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

// adjustPathForDocker converts paths for Windows/WSL2; on macOS (darwin) no change is needed.
func adjustPathForDockerWithOS(path, goos string, isWSL bool) string {
	if goos == "darwin" {
		// macOS uses Unix-style paths.
		return path
	} else if goos == "windows" {
		// Convert Unix-like paths (/mnt/c/...) to Windows-style (C:\...)
		if strings.HasPrefix(path, "/mnt/") {
			path = strings.ReplaceAll(path, "/mnt/", "")
			path = strings.ReplaceAll(path, "/", "\\")
			path = strings.ToUpper(path[:1]) + ":" + path[1:]
		}
	} else if isWSL {
		// Convert Windows-style paths (C:\...) to WSL-compatible paths (/mnt/c/...)
		if len(path) > 1 && path[1] == ':' {
			drive := strings.ToLower(string(path[0]))
			path = fmt.Sprintf("/mnt/%s%s", drive, strings.ReplaceAll(path[2:], "\\", "/"))
		}
	}
	return path
}

func adjustPathForDocker(path string) string {
  return adjustPathForDockerWithOS(path, runtime.GOOS, isWSL2())
}

func isWSL2() bool {
	_, isWSL := os.LookupEnv("WSL_INTEROP")
	return isWSL && runtime.GOOS == "linux"
}

// startContainer creates and starts the Docker container running Hugo.
func startContainer(ctx context.Context, cli *client.Client, cfg Config) (string, error) {
	// Adjust the path for mounting.
	userRepoPath := adjustPathForDocker(cfg.WatchDir)
	mounts := []mount.Mount{
		{
			Type:   mount.TypeBind,
			Source: userRepoPath,
			Target: "/home/UserRepo",
		},
	}

	// Mount the Hugo configuration file.
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

	// Prepare the container configuration.
	containerConfig := &container.Config{
		Image: cfg.DockerImage,
		Cmd:   []string{"server", "--bind", "0.0.0.0", "--liveReload", "--disableFastRender", "--poll"},
		Tty:   true,
		ExposedPorts: nat.PortSet{
			nat.Port(cfg.ContainerPort + "/tcp"): struct{}{},
		},
	}
	hostConfig := &container.HostConfig{
		Mounts: mounts,
		PortBindings: nat.PortMap{
			nat.Port(cfg.ContainerPort+"/tcp"): []nat.PortBinding{
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

// attachContainer attaches to the container's I/O streams.
func attachContainer(ctx context.Context, cli *client.Client, containerID string) error {
	opts := container.AttachOptions{
		Stream: true,
		Stdout: true,
		Stderr: true,
		Stdin:  true,
	}
	resp, err := cli.ContainerAttach(ctx, containerID, opts)
	if err != nil {
		return fmt.Errorf("container attach error: %w", err)
	}
	// Copy container output to os.Stdout.
	go func() {
		_, _ = io.Copy(os.Stdout, resp.Reader)
	}()
	// Forward os.Stdin to the container.
	go func() {
		_, _ = io.Copy(resp.Conn, os.Stdin)
	}()
	return nil
}

// stopAndRemoveContainer stops and removes the specified container.
func stopAndRemoveContainer(cli *client.Client, containerID string) {
	fmt.Printf("Stopping container: %s\n", containerID)
        timeout := 10
	stopOpts := container.StopOptions{Timeout: &timeout}
	if err := cli.ContainerStop(context.Background(), containerID, stopOpts); err != nil {
		fmt.Printf("Error stopping container %s: %v\n", containerID, err)
	}
	if err := cli.ContainerRemove(context.Background(), containerID, container.RemoveOptions{Force: true}); err != nil {
		fmt.Printf("Error removing container %s: %v\n", containerID, err)
	}
}

// watchAndRestart monitors the watch directory (recursively) and restarts the container after a debounce interval.
func watchAndRestart(ctx context.Context, cli *client.Client, cfg Config, containerID *string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("Error creating file watcher: %v\n", err)
		return
	}
	defer watcher.Close()

	// Add watchers recursively for all subdirectories.
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

	fmt.Println("Watching for file changes in:", cfg.WatchDir)
	debounceDuration := 2 * time.Second
	var debounceTimer *time.Timer

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Look for write, create, or remove events.
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				fmt.Println("File change detected:", event.Name)
				// Restart the debounce timer on each event.
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.NewTimer(debounceDuration)
			}
		case <-func() <-chan time.Time {
			if debounceTimer != nil {
				return debounceTimer.C
			}
			// If no timer is active, block indefinitely.
			ch := make(chan time.Time)
			return ch
		}():
			fmt.Println("Restarting container due to file changes")
			stopAndRemoveContainer(cli, *containerID)
			newID, err := startContainer(ctx, cli, cfg)
			if err != nil {
				fmt.Printf("Error restarting container: %v\n", err)
			} else {
				*containerID = newID
				if err := attachContainer(ctx, cli, newID); err != nil {
					fmt.Printf("Error attaching to container: %v\n", err)
				}
			}
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

func main() {
	cfg := getConfig()
	ctx := context.Background()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Printf("Error creating Docker client: %v\n", err)
		os.Exit(1)
	}

	// Start the Hugo container interactively.
	containerID, err := startContainer(ctx, cli, cfg)
	if err != nil {
		fmt.Printf("Error starting container: %v\n", err)
		os.Exit(1)
	}

	// Attach to the container.
	if err := attachContainer(ctx, cli, containerID); err != nil {
		fmt.Printf("Error attaching container: %v\n", err)
		os.Exit(1)
	}

	// Setup signal handling (CTRL+C) to clean up.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nReceived shutdown signal. Stopping container.")
		stopAndRemoveContainer(cli, containerID)
		os.Exit(0)
	}()

	// Start the file watcher to monitor changes and restart the container when needed.
	watchAndRestart(ctx, cli, cfg, &containerID)
}


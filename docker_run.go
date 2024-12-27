package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func main() {
        // Check Docker running
        checkDockerAvailable()

	// Validate arguments
	if len(os.Args) < 2 {
		fmt.Println("Usage: docker_run [ build | server | generate_toml | update_scripts | update_fdevsec | shell ]")
		os.Exit(1)
	}
	command := os.Args[1]

	// Get current directory and adjust paths
	currentDir, err := filepath.Abs(".")
	if err != nil {
		fmt.Printf("Error getting current directory: %v\n", err)
		os.Exit(1)
	}
	userRepoPath := adjustPathForDocker(currentDir)
	centralRepoPath := adjustPathForDocker(filepath.Join(currentDir, "hugo.toml"))

	var cmdArgs []string
	switch command {
	case "server", "shell", "build":
		cmdArgs = []string{
			"run",
			"--rm",
			"-it",
			"-v", fmt.Sprintf("%s:/home/UserRepo", userRepoPath),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/home/CentralRepo/hugo.toml", centralRepoPath),
			"-p", "1313:1313",
			"fortinet-hugo:latest",
			command, "--disableFastRender", "--poll",
		}
	case "generate_toml", "update_scripts", "update_fdevsec":
		cmdArgs = []string{
			"run",
			"--rm",
			"-it",
			"-v", fmt.Sprintf("%s:/home/UserRepo", userRepoPath),
			"fortinet-hugo:latest",
			command,
		}
	default:
		fmt.Println("Invalid command.")
		os.Exit(1)
	}

	fmt.Println("**** Here's the docker run command we're using: ****")
	fmt.Printf("docker %s\n", strings.Join(cmdArgs, " "))
	err = executeDockerCommand(cmdArgs)
	if err != nil {
		fmt.Printf("Error executing Docker command: %v\n", err)
		os.Exit(1)
	}
}

// adjustPathForDocker converts paths for WSL2 or native Windows environments
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

// isWSL2 detects if the program is running inside WSL2
func isWSL2() bool {
	_, isWSL := os.LookupEnv("WSL_INTEROP")
	return isWSL && runtime.GOOS == "linux"
}

// executeDockerCommand runs the Docker command with the provided arguments
func executeDockerCommand(args []string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func checkDockerAvailable() {
  _, err := exec.LookPath("docker")
  if err != nil {
    fmt.Println("Docker is not installed or not found in PATH.")
    os.Exit(1)
  }
}


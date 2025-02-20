package main

import (
  "os"
  "runtime"
  "testing"
)

func TestAdjustPathForDocker_Darwin(t *testing.T){
  //On macOS, no conversion should occur.
  input := "/Users/test/project"
  expected := "/Users/test/project"
  result := adjustPathForDockerWithOS(input, "darwin", false)
  if result != expected {
    t.Errorf("Darwin: Expected %s, got %s", expected, result)
  }
}

func TestAdjustPathForDocker_Windows(t *testing.T){
  //On Windows, convert a Unix-style path to a Windows-style path.
  input := "/mnt/c/Users/test"
  expected := "C:\\Users\\test"
  result := adjustPathForDockerWithOS(input, "windows", false)
  if result != expected {
    t.Errorf("Windows: Expected %s, got %s", expected, result)
  }
}

func TestAdjustPathForDocker_WSL2(t *testing.T){
  //In WSL2 environment, convert a Windows-style path to a WSL2 path.
  input := "C:\\Users\\test"
  expected := "/mnt/c/Users/test"
  result := adjustPathForDockerWithOS(input, "linux", true)
  if result != expected {
    t.Errorf("WSL2: Expected %s, got %s", expected, result)
  }
}

func TestGetConfigDefaults(t *testing.T) {
	// Clear environment variables to force defaults.
  os.Unsetenv("DOCKER_IMAGE")
  os.Unsetenv("HOST_PORT")
  os.Unsetenv("CONTAINER_PORT")
  os.Unsetenv("WATCH_DIR")
  
  cfg := getConfig()
  
  if cfg.DockerImage != "fortinet-hugo:latest" {
  	t.Errorf("Expected docker image default 'fortinet-hugo:latest', got %s", cfg.DockerImage)
  }
  if cfg.HostPort != "1313" {
  	t.Errorf("Expected host port default '1313', got %s", cfg.HostPort)
  }
  if cfg.ContainerPort != "1313" {
  	t.Errorf("Expected container port default '1313', got %s", cfg.ContainerPort)
  }
  // WATCH_DIR will be set to the current directory's absolute path.
  if cfg.WatchDir == "" {
  	t.Errorf("Expected WATCH_DIR to be set to a valid directory, got empty string")
  }
}

func TestIsWSL2(t *testing.T) {
  // Save current value and restore at the end.
  origVal, existed := os.LookupEnv("WSL_INTEROP")
  defer func() {
    if existed {
      os.Setenv("WSL_INTEROP", origVal)
    } else {
      os.Unsetenv("WSL_INTEROP")
    }
  }()
  
  os.Unsetenv("WSL_INTEROP")
  if isWSL2() {
    t.Error("Expected isWSL2 to be false when WSL_INTEROP is not set")
  }
  
  os.Setenv("WSL_INTEROP", "dummy")
  // isWSL2 should return true only if runtime.GOOS is "linux".
  expected := (runtime.GOOS == "linux")
  if isWSL2() != expected {
    t.Errorf("Expected isWSL2 to be %v when WSL_INTEROP is set on OS %s", expected, runtime.GOOS)
  }
}


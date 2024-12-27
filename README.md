## FortinetCloudCSE Docker Development Helper

Prereqs:

- Docker installed (via Rancher Desktop, for example)
- Go installed 
  - For instructions on installing Go, head here: https://go.dev/doc/install
- Workshop Docker image built

## To build the CLI tool:

*Initialize the Go module:*
```
go mod init docker-run-helper
```

*Build:*
- **Linux:**
```
GOOS=linux GOARCH=amd64 go build -o docker_run .
```
- **macOS:**
```
GOOS=darwin GOARCH=amd64 go build -o docker_run .
```
- **Windows:**
```
GOOS=windows GOARCH=amd64 go build -o docker_run.exe .

```

*Update executable permissions if needed:*
```
chmod +x docker_run
```

*Copy the executable into a directory in the system path. To list the path, run:*

- In bash (linux or mac):
```
echo $PATH 
```

- In windows:
```
echo %PATH% // Windows
```

## To Run, Build, or Get a Shell In the Container:

*From your workshop directory, run:*

```
docker_run server

docker_run build

docker_run shell

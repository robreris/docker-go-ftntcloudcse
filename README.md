## FortinetCloudCSE Docker Development Helper

## To run the tool via pre-compiled binary:

Navigate to the *binaries* folder above, click the binary for your OS/Architecture, and click on the **download raw file** icon at the top right of the screen. 

Then, you can either copy the binary into your system path or to the local directory containing your workshop.

To get your system path:

- In bash (linux or mac):
```
echo $PATH 
```

- In windows:
```
echo %PATH% // Windows
```

The binary will look for the following environment variables. If you don't set them in your current shell, defaults will be set as below:

| Environment Variable | Default Setting      |
| -------------------- | -------------------- |
| DOCKER_IMAGE         | fortinet-hugo:latest |
| HOST_PORT            | 1313                 |
| CONTAINER_PORT       | 1313                 |
| WATCH_DIR            | . (current directory)|


*From your workshop directory, run:*

```
./docker_run

(or)

C:\docker_run.exe


## To build the CLI tool:

**Prereqs**:

- Docker installed (via Rancher Desktop, for example)
- Go installed (not needed if you just want to run the compiled binary)
  - For instructions on installing Go, head here: https://go.dev/doc/install
- Workshop Docker image built

*Download necessary go libraries:*
```
go get -u
```

*Initialize the Go module:*
```
go mod init docker-run-helper
```

*Build:*

**Note: Before building, you can confirm availability of the desired OS/Architecture via:**
```
go tool dist list
``` 

- **Linux/x86_64:**
```
GOOS=linux GOARCH=amd64 go build -o docker_run .
```
- **macOS/AMD64:**
```
GOOS=darwin GOARCH=amd64 go build -o docker_run .
```
- **Windows/x86_64:**
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

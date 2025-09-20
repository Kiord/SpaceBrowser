GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/spacebrowser-linux-amd64 .    
GOOS=linux   GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/spacebrowser-linux-arm64 .
GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/spacebrowser-darwin-amd64 .
GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/spacebrowser-darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w -H=windowsgui" -o dist/spacebrowser-windows-amd64.exe .

//go:build linux

package platform

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const linuxMountInfoPath = "/proc/self/mountinfo"

func (Linux) IsMountRoot(path string) bool {
	file, err := os.Open(linuxMountInfoPath)
	if err != nil {
		return false
	}
	defer file.Close()
	root, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	_, found := linuxMountPoints(file)[filepath.Clean(root)]
	return found
}

func linuxMountPoints(reader io.Reader) map[string]struct{} {
	result := make(map[string]struct{})
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			continue
		}
		mountPoint, ok := decodeLinuxMountInfoPath(fields[4])
		if ok {
			result[filepath.Clean(mountPoint)] = struct{}{}
		}
	}
	return result
}

func decodeLinuxMountInfoPath(value string) (string, bool) {
	var decoded strings.Builder
	for index := 0; index < len(value); {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			index++
			continue
		}
		if index+3 >= len(value) {
			return "", false
		}
		number, err := strconv.ParseUint(value[index+1:index+4], 8, 8)
		if err != nil {
			return "", false
		}
		decoded.WriteByte(byte(number))
		index += 4
	}
	return decoded.String(), true
}

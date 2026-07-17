package platform

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Command describes an executable and its arguments without starting it.
type Command struct {
	Name string
	Args []string
}

// RevealCommand builds the platform-specific command used to reveal a file.
func RevealCommand(goos string, path string) (Command, error) {
	if strings.TrimSpace(path) == "" {
		return Command{}, fmt.Errorf("文件路径不能为空")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Command{}, err
	}

	switch goos {
	case "darwin":
		return Command{Name: "open", Args: []string{"-R", absolutePath}}, nil
	case "windows":
		return Command{Name: "explorer", Args: []string{"/select,", absolutePath}}, nil
	case "linux":
		return Command{Name: "xdg-open", Args: []string{filepath.Dir(absolutePath)}}, nil
	default:
		return Command{}, fmt.Errorf("不支持的操作系统: %s", goos)
	}
}

// Reveal starts the native file manager at path.
func Reveal(path string) error {
	command, err := RevealCommand(runtime.GOOS, path)
	if err != nil {
		return err
	}
	return exec.Command(command.Name, command.Args...).Start()
}

package platform

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRevealCommand(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "video.mp4")
	absolutePath, err := filepath.Abs(filePath)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		goos string
		want Command
	}{
		{
			name: "macOS selects file in Finder",
			goos: "darwin",
			want: Command{Name: "open", Args: []string{"-R", absolutePath}},
		},
		{
			name: "Windows selects file in Explorer",
			goos: "windows",
			want: Command{Name: "explorer", Args: []string{"/select,", absolutePath}},
		},
		{
			name: "Linux opens containing directory",
			goos: "linux",
			want: Command{Name: "xdg-open", Args: []string{filepath.Dir(absolutePath)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RevealCommand(tt.goos, filePath)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("RevealCommand() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestRevealCommandRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := RevealCommand("plan9", "video.mp4"); err == nil {
		t.Fatal("RevealCommand() error = nil, want unsupported platform error")
	}
}

func TestRevealCommandMakesPathAbsolute(t *testing.T) {
	relativePath := filepath.Join("downloads", "video.mp4")
	absolutePath, err := filepath.Abs(relativePath)
	if err != nil {
		t.Fatal(err)
	}
	command, err := RevealCommand("darwin", relativePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := command.Args[1]; got != absolutePath {
		t.Fatalf("revealed path = %q, want absolute path %q", got, absolutePath)
	}
}

func TestRevealCommandRejectsEmptyPath(t *testing.T) {
	if _, err := RevealCommand("darwin", " "); err == nil {
		t.Fatal("RevealCommand() error = nil, want empty path error")
	}
}

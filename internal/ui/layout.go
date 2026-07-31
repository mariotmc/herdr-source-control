package ui

import (
	"os"
	"path/filepath"
	"strings"
)

type Size uint8

const (
	SizeMinimum Size = iota
	SizeSmall
	SizeNarrow
	SizeWide
)

func Classify(width, height int) Size {
	if width < 40 || height < 12 {
		return SizeMinimum
	}
	if width < 60 || height < 18 {
		return SizeSmall
	}
	if width < 96 || height < 24 {
		return SizeNarrow
	}
	return SizeWide
}

func DisplayRoot(root string) string {
	home, err := os.UserHomeDir()
	if err == nil && (root == home || strings.HasPrefix(root, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(root, home)
	}
	return root
}

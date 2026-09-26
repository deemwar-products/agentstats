package render

import (
	"bytes"
	"fmt"
	"os/exec"
)

// PNG rasterises an SVG by piping it through the resvg binary (bundled in the
// container image, with the Inter font). Returns an error when resvg is not on
// PATH — the CLI treats PNGs as best-effort locally; the container always has it.
func PNG(svg []byte) ([]byte, error) {
	if _, err := exec.LookPath("resvg"); err != nil {
		return nil, fmt.Errorf("resvg not found on PATH: %w", err)
	}
	// resvg reads the SVG from stdin ("-") and writes the PNG to stdout ("-c").
	cmd := exec.Command("resvg", "-", "-c")
	cmd.Stdin = bytes.NewReader(svg)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("resvg: %v: %s", err, errBuf.String())
	}
	return out.Bytes(), nil
}

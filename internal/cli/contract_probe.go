package cli

import (
	"context"
	"os/exec"
	"time"
)

func probeContractVersion(binary string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	return string(out), err
}

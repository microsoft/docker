//+build windows

package pshell

import (
	"bytes"
	"os/exec"
	"strings"

	log "github.com/Sirupsen/logrus"
)

func ExecutePowerShell(script string) (string, error) {
	cmd := exec.Command("powershell", "-command", "-")
	cmd.Stdin = strings.NewReader(script)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		log.Errorln("Unable to execute PowerShell: ", err.Error())
		return "", err
	}
	return out.String(), nil
}

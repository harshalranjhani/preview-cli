package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"golang.org/x/term"
)

func parsePort(raw string) (int, error) {
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, clierr.Usage("port must be a number from 1 to 65535")
	}
	return port, nil
}

func promptLine(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return "", err
		}
		return "", nil
	}
	return strings.TrimSpace(sc.Text()), nil
}

func promptSecret(label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	if !isTerminal() {
		return promptLine("")
	}
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(value)), nil
}

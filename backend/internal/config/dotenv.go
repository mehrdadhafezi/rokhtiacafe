package config

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// LoadDotEnv sets variables from the first existing file in paths. Variables already present
// in the environment win, so real deployments are never overridden by a stray file.
// It returns the path that was loaded, or "" if none existed.
func LoadDotEnv(paths ...string) (string, error) {
	for _, p := range paths {
		f, err := os.Open(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		for n := 1; sc.Scan(); n++ {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				return "", fmt.Errorf("%s:%d: expected KEY=VALUE", p, n)
			}
			key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
			val = strings.TrimSpace(val)
			if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
				val = val[1 : len(val)-1]
			}
			if _, exists := os.LookupEnv(key); !exists {
				if err := os.Setenv(key, val); err != nil {
					return "", err
				}
			}
		}
		return p, sc.Err()
	}
	return "", nil
}

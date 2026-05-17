package compat

import (
	"fmt"
	"strconv"
	"strings"
)

type Finding struct {
	Code    string `json:"code"`
	Summary string `json:"summary"`
}

func SatisfiesMinimum(current, minimum string) (bool, error) {
	current = normalize(current)
	minimum = normalize(minimum)
	if minimum == "" {
		return true, nil
	}
	if current == "" {
		return false, nil
	}
	if current == "dev" || minimum == "dev" {
		return true, nil
	}
	currentVersion, err := parse(current)
	if err != nil {
		return false, err
	}
	minimumVersion, err := parse(minimum)
	if err != nil {
		return false, err
	}
	return compare(currentVersion, minimumVersion) >= 0, nil
}

func CheckRunnerRelease(runnerVersion, minimum string) []Finding {
	ok, err := SatisfiesMinimum(runnerVersion, minimum)
	if err != nil {
		return []Finding{{Code: "RUNNER_VERSION_INVALID", Summary: err.Error()}}
	}
	if !ok {
		return []Finding{{Code: "RUNNER_VERSION_TOO_OLD", Summary: fmt.Sprintf("runner version %q is older than required %q", runnerVersion, minimum)}}
	}
	return nil
}

func CheckClientServer(cliVersion, serverVersion string) []Finding {
	cliVersion = normalize(cliVersion)
	serverVersion = normalize(serverVersion)
	if cliVersion == "" || serverVersion == "" || cliVersion == "dev" || serverVersion == "dev" {
		return nil
	}
	cli, err := parse(cliVersion)
	if err != nil {
		return []Finding{{Code: "CLI_VERSION_INVALID", Summary: err.Error()}}
	}
	server, err := parse(serverVersion)
	if err != nil {
		return []Finding{{Code: "SKIFFD_VERSION_INVALID", Summary: err.Error()}}
	}
	if compare(server, cli) < 0 {
		return []Finding{{Code: "SKIFFD_VERSION_OLDER_THAN_CLI", Summary: fmt.Sprintf("skiffd version %q is older than CLI version %q", serverVersion, cliVersion)}}
	}
	return nil
}

type version [3]int

func parse(value string) (version, error) {
	value = normalize(value)
	if idx := strings.IndexAny(value, "-+"); idx >= 0 {
		value = value[:idx]
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return version{}, fmt.Errorf("version %q must be major.minor.patch", value)
	}
	var out version
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version{}, fmt.Errorf("version %q has invalid component %q", value, part)
		}
		out[i] = n
	}
	return out, nil
}

func compare(a, b version) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func normalize(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "v"))
	return value
}

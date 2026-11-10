// Package quickstart extracts the published-release shell block from the
// README and validates the chart reference and version it names.
package quickstart

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Block is one shell block tagged quickstart.
type Block struct {
	Command string
	Line    int
}

// Plan is the executable part of the README quickstart.
type Plan struct {
	Commands []string
	ChartRef string
	Version  string
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// Extract returns the shell fenced blocks whose info string contains
// quickstart. Untagged fences are skipped, but must still be closed.
func Extract(markdown []byte) ([]Block, error) {
	var blocks []Block
	scanner := bufio.NewScanner(bytes.NewReader(markdown))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		inFence   bool
		tagged    bool
		fenceChar byte
		fenceLen  int
		fenceLine int
		content   []string
		lineNo    int
	)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if !inFence {
			trimmed := strings.TrimLeft(line, " ")
			if len(line)-len(trimmed) > 3 {
				continue
			}
			char, count, info, ok := fenceStart(trimmed)
			if !ok {
				continue
			}
			fields := strings.Fields(info)
			tagged = contains(fields, "quickstart")
			if tagged && (len(fields) == 0 || (fields[0] != "sh" && fields[0] != "bash" && fields[0] != "shell")) {
				return nil, fmt.Errorf("line %d: quickstart block must be a shell block", lineNo)
			}
			inFence = true
			fenceChar = char
			fenceLen = count
			fenceLine = lineNo
			content = content[:0]
			continue
		}
		if fenceEnd(line, fenceChar, fenceLen) {
			if tagged {
				command := strings.TrimRight(strings.Join(content, "\n"), "\n")
				if strings.TrimSpace(command) == "" {
					return nil, fmt.Errorf("line %d: quickstart block is empty", fenceLine)
				}
				blocks = append(blocks, Block{Command: command, Line: fenceLine})
			}
			inFence = false
			tagged = false
			content = content[:0]
			continue
		}
		if tagged {
			content = append(content, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if inFence {
		if tagged {
			return nil, fmt.Errorf("line %d: unclosed quickstart fence", fenceLine)
		}
		return nil, fmt.Errorf("line %d: unclosed fence", fenceLine)
	}
	if len(blocks) == 0 {
		return nil, errors.New("no quickstart-tagged shell blocks")
	}
	return blocks, nil
}

// Build validates the extracted blocks and returns the commands, chart
// reference, and version the doc-test will use.
func Build(blocks []Block) (Plan, error) {
	if len(blocks) == 0 {
		return Plan{}, errors.New("no quickstart-tagged shell blocks")
	}
	refs := map[string]bool{}
	versions := map[string]bool{}
	installs := false
	commands := make([]string, 0, len(blocks))
	for _, block := range blocks {
		fields := strings.Fields(normalizeContinuations(block.Command))
		if contains(fields, "helm") && (contains(fields, "install") || contains(fields, "upgrade")) {
			installs = true
		}
		for index := 0; index < len(fields); index++ {
			field := trimQuotes(fields[index])
			switch {
			case strings.HasPrefix(field, "oci://"):
				refs[field] = true
			case field == "--version":
				value := ""
				if index+1 < len(fields) {
					value = trimQuotes(fields[index+1])
					index++
				}
				versions[value] = true
			case strings.HasPrefix(field, "--version="):
				versions[trimQuotes(strings.TrimPrefix(field, "--version="))] = true
			}
		}
		commands = append(commands, block.Command)
	}
	if !installs {
		return Plan{}, errors.New("quickstart must run helm install or helm upgrade")
	}
	if len(refs) != 1 {
		return Plan{}, fmt.Errorf("quickstart must reference exactly one oci:// chart, found %d", len(refs))
	}
	if len(versions) == 0 {
		return Plan{}, errors.New("quickstart must set exactly one --version, found 0")
	}
	if len(versions) > 1 {
		values := sortedKeys(versions)
		return Plan{}, fmt.Errorf("quickstart uses multiple chart versions: %s", strings.Join(values, ", "))
	}
	versionsList := sortedKeys(versions)
	version := versionsList[0]
	if !semver.MatchString(version) {
		return Plan{}, fmt.Errorf("invalid chart version %q", version)
	}
	ref := sortedKeys(refs)[0]
	if err := validateChartRef(ref); err != nil {
		return Plan{}, err
	}
	return Plan{Commands: commands, ChartRef: ref, Version: version}, nil
}

func validateChartRef(ref string) error {
	parsed, err := url.Parse(ref)
	if err != nil {
		return fmt.Errorf("invalid chart reference %q: %w", ref, err)
	}
	if parsed.Scheme != "oci" || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return fmt.Errorf("invalid chart reference %q", ref)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("invalid chart reference %q: query and fragment are not allowed", ref)
	}
	return nil
}

func normalizeContinuations(command string) string {
	command = strings.ReplaceAll(command, "\\\r\n", " ")
	return strings.ReplaceAll(command, "\\\n", " ")
}

func fenceStart(trimmed string) (byte, int, string, bool) {
	if trim := strings.TrimSpace(trimmed); trim == "" {
		return 0, 0, "", false
	}
	char := trimmed[0]
	if char != '`' && char != '~' {
		return 0, 0, "", false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == char {
		count++
	}
	if count < 3 {
		return 0, 0, "", false
	}
	return char, count, strings.TrimSpace(trimmed[count:]), true
}

func fenceEnd(line string, char byte, minimum int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == char {
		count++
	}
	if count < minimum {
		return false
	}
	return strings.TrimSpace(trimmed[count:]) == ""
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func trimQuotes(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

package run

import "strings"

func emptyFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return strings.TrimSpace(s)
}

func extractMarkdownSectionItems(markdown string, sectionNames ...string) []string {
	wanted := map[string]bool{}
	for _, name := range sectionNames {
		wanted[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var out []string
	inWanted := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "## "):
			name := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			inWanted = wanted[strings.ToLower(name)]
			continue
		case strings.HasPrefix(trimmed, "#"):
			inWanted = false
			continue
		}
		if !inWanted {
			continue
		}
		if item := markdownListItem(trimmed); item != "" {
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		for _, line := range strings.Split(markdown, "\n") {
			if item := markdownListItem(strings.TrimSpace(line)); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

func markdownListItem(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	for i, r := range line {
		if r < '0' || r > '9' {
			if i > 0 && (strings.HasPrefix(line[i:], ". ") || strings.HasPrefix(line[i:], ") ")) {
				return strings.TrimSpace(line[i+2:])
			}
			break
		}
	}
	return ""
}

func splitTaskField(line string) (field, value string, ok bool) {
	before, after, found := strings.Cut(line, ":")
	if !found {
		return "", "", false
	}
	field = strings.TrimSpace(before)
	if field == "" {
		return "", "", false
	}
	return field, strings.TrimSpace(after), true
}

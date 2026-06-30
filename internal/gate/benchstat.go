package gate

import "strings"

// proofLabel maps a hypothesis proof metric (ns_op|B_op|allocs_op) to its benchstat column label.
func proofLabel(metric string) string {
	switch metric {
	case "ns_op":
		return "sec/op"
	case "B_op":
		return "B/op"
	case "allocs_op":
		return "allocs/op"
	}
	return ""
}

// parseBenchstat reads benchstat `-format csv` and returns ("vs base", p) for one metric.
// A metric section starts with a header row `,<label>,CI,<label>,CI,vs base,P`; the next data
// row (col 0 non-empty, not "geomean") carries vs base at col len-2 and "p=.. n=.." at col len-1.
// vs base is "~" when not significant, else a signed percentage like "-19.04%".
func parseBenchstat(csv, label string) (vsBase, p string) {
	inSection := false
	for _, line := range strings.Split(csv, "\n") {
		cols := strings.Split(line, ",")
		if strings.Contains(line, ","+label+",CI") {
			inSection = true
			continue
		}
		if strings.TrimSpace(line) == "" {
			inSection = false
			continue
		}
		if inSection && len(cols) >= 7 && cols[0] != "" && cols[0] != "geomean" {
			vsBase = cols[len(cols)-2]
			p = strings.TrimPrefix(cols[len(cols)-1], "p=")
			if i := strings.Index(p, " "); i >= 0 {
				p = p[:i]
			}
			return vsBase, p
		}
	}
	return "", ""
}

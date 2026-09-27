package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/state"
	"github.com/harshalranjhani/preview-cli/internal/ui"
)

func printPreviewTable(w interface{ Write([]byte) (int, error) }, items []state.Preview) error {
	if len(items) == 0 {
		fmt.Fprintln(w, "No previews")
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tPROJECT\tNAME\tTARGET\tURL\tEXPIRES")
	now := time.Now().UTC()
	for _, item := range items {
		name := item.Name
		if name == "" {
			name = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID,
			item.Project,
			name,
			item.Dial(),
			ui.Truncate(item.PublicURL(), 52),
			ui.FormatRemaining(item.ExpiresAt, now),
		)
	}
	return tw.Flush()
}

func step(ok bool, message string) {
	if flagJSON {
		return
	}
	if ok {
		fmt.Printf("✓ %s\n", message)
		return
	}
	fmt.Printf("✗ %s\n", message)
}

func dnsInstructions(domain, ipv4, ipv6 string) {
	if flagJSON {
		return
	}
	fmt.Printf("\nCreate these DNS records and leave them DNS-only (not proxied):\n\n")
	if ipv4 == "" {
		ipv4 = "<this server's public IPv4>"
	}
	fmt.Printf("  *.%s.   A      %s\n", domain, ipv4)
	if ipv6 != "" {
		fmt.Printf("  *.%s.   AAAA   %s\n", domain, ipv6)
	}
	fmt.Printf("\nCaddy will request a wildcard certificate for *.%s after the record is visible.\n", domain)
}

package main

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/internal/lsp"
	"github.com/spf13/cobra"
)

var lspCmd = &cobra.Command{
	Use:         "lsp",
	Annotations: map[string]string{longLived: "true"},
	Short:       "Start the SNGL language server",
	RunE: func(cmd *cobra.Command, args []string) error {
		srv := lsp.New()
		tcp, _ := cmd.Flags().GetString("tcp")
		if tcp != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "sngl lsp: listening on %s\n", tcp)
			return srv.RunTCP(tcp)
		}
		return srv.RunStdio()
	},
}

func init() {
	lspCmd.Flags().String("tcp", "", "listen on TCP address (e.g. :7998) instead of stdio")
}

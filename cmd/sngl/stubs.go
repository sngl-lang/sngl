package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func stubCmd(name, short string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: short,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("%s: not yet implemented", name)
		},
	}
}

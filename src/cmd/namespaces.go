package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/conf"
)

func newNamespacesCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ns",
		Aliases: []string{"namespaces"},
		Short:   "Show the namespaces this node serves",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := conf.Load()
			if err != nil {
				return err
			}

			if cfg.Path == "" {
				fmt.Printf("no config file; serving the defaults\n\n")
			} else {
				fmt.Printf("%s\n\n", cfg.Path)
			}
			for _, m := range cfg.Mounts.All() {
				fmt.Printf("  %-24s %-7s %s\n", m.Path, m.Kind, detail(m))
			}
			return nil
		},
	}
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/bresilla/drop/src/pkg/book"
	"github.com/bresilla/drop/src/pkg/convo"
)

func newLogCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "log [name|peer-id]",
		Short: "Show a conversation, or list them all",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			pinned, err := book.Load()
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return listConversations(pinned)
			}

			entry, err := book.Resolve(args[0])
			if err != nil {
				return err
			}
			store, err := convo.Open(entry.ID)
			if err != nil {
				return err
			}
			history, err := store.History()
			if err != nil {
				return err
			}
			if len(history) == 0 {
				fmt.Printf("nothing said to %s yet\n", entry.Name)
				return nil
			}

			if limit > 0 && len(history) > limit {
				history = history[len(history)-limit:]
			}
			for _, m := range history {
				fmt.Println(render(entry.Name, m))
			}

			waiting, err := store.Pending()
			if err != nil {
				return err
			}
			if len(waiting) > 0 {
				fmt.Printf("\n%d message(s) still waiting to be delivered\n", len(waiting))
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "how many messages to show; 0 for all")

	return cmd
}

func listConversations(pinned *book.Book) error {
	peers, err := convo.Peers()
	if err != nil {
		return err
	}
	if len(peers) == 0 {
		fmt.Println("no conversations yet")
		return nil
	}

	for _, id := range peers {
		store, err := convo.Open(id)
		if err != nil {
			continue
		}
		history, err := store.History()
		if err != nil {
			continue
		}
		waiting, _ := store.Pending()

		last := ""
		if len(history) > 0 {
			last = history[len(history)-1].Body
			if len(last) > 40 {
				last = last[:39] + "…"
			}
		}

		note := ""
		if len(waiting) > 0 {
			note = fmt.Sprintf("  (%d waiting)", len(waiting))
		}
		fmt.Printf("  %-16s %3d  %s%s\n", nameFor(pinned, id), len(history), last, note)
	}
	return nil
}

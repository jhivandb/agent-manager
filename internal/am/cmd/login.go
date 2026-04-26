package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func NewLoginCmd() *cobra.Command {
	var url string
	var name string
	var clientID string
	var clientSecret string

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to an instance",
		Long:  ``,
		RunE: func(cmd *cobra.Command, args []string) error {
			if url == "" {
				return fmt.Errorf("--url is required")
			}

			if clientID == "" || clientSecret == "" {
				return fmt.Errorf("--client-id and --client-secret are required")
			}

			if name == "" {
				name = "default"
			}
			// TODO: call auth logic here
			fmt.Fprintf(cmd.OutOrStdout(), "login url=%s name=%s clientID=%s\n", url, name, clientID)

			return nil
		},
	}

	cmd.Flags().StringVar(&url, "url", "", "Agent Manager instance URL")
	cmd.Flags().StringVar(&name, "name", "", "Agent Manager instance name")
	cmd.Flags().StringVar(&clientID, "client-id", "", "OAuth client ID")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "Oauth client secret")

	return cmd
}

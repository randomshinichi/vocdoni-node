package commands

import (
	"fmt"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"go.vocdoni.io/dvote/api"
	"go.vocdoni.io/dvote/client"
)

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.AddCommand(accountCmd)
	rootCmd.AddCommand(sendCmd)
	rootCmd.AddCommand(claimFaucetCmd)
	rootCmd.AddCommand(genFaucetCmd)
	rootCmd.AddCommand(mintCmd)
	rootCmd.AddCommand(keysCmd)
	keysCmd.AddCommand(keysNewCmd)
	keysCmd.AddCommand(keysImportCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysChangePasswordCmd)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}

var rootCmd = &cobra.Command{
	Use:   "vocli",
	Short: "vocli is a convenience CLI that helps you do things on Vochain",
}

var sendCmd = &cobra.Command{
	Use:   "send [sender_json_keystore_path, amount, recipient]",
	Short: "Send tokens to another account",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var claimFaucetCmd = &cobra.Command{
	Use:   "claimfaucet [some arguments?]",
	Short: "Claim tokens from another account, using a payload generated from that account that acts as an authorization.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var genFaucetCmd = &cobra.Command{
	Use:   "genfaucet [some arguments?]",
	Short: "Generate a payload allowing another account to claim tokens from this account.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var mintCmd = &cobra.Command{
	Use:   "mint [some arguments?]",
	Short: "Mint more tokens. Only the Treasurer may do this.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nil
	},
}

var accountCmd = &cobra.Command{
	Use:   "info",
	Short: "Get information about an account. The address may or may not include the 0x prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := client.New("https://gw1.dev.vocdoni.net/dvote")
		if err != nil {
			return err
		}
		req := &api.APIrequest{}
		req.EntityId = common.HexToAddress(args[0]).Bytes()
		req.Method = "getAccount"
		resp, err := client.Request(*req, nil)
		if err != nil {
			return err
		} else if resp.Nonce == nil {
			fmt.Println("account", args[0], "does not exist")
			return err
		}
		fmt.Println("resp", resp.String(), "err", err)
		return err
	},
}

package commands

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"go.vocdoni.io/dvote/client"
	"go.vocdoni.io/dvote/log"
	"go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

var gatewayRpc string
var debug bool
var infoUri string
var nonce uint32
var home string
var password string
var Stdout io.Writer
var Stderr io.Writer
var Stdin *os.File

func init() {
	Stdout = os.Stdout
	Stderr = os.Stderr
	RootCmd.CompletionOptions.DisableDefaultCmd = true
	RootCmd.PersistentFlags().StringVarP(&gatewayRpc, "url", "u", "https://gw1.dev.vocdoni.net/dvote", "Gateway RPC URL")
	RootCmd.PersistentFlags().StringVar(&home, "home", "", "root directory where all vochain files are stored (normally ~/.dvote)")
	RootCmd.PersistentFlags().StringVar(&password, "password", "", "supply the password as an argument instead of prompting")
	RootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "prints additional information")
	RootCmd.PersistentFlags().Uint32VarP(&nonce, "nonce", "n", 0, "account nonce to use when sending transaction (useful when it cannot be queried ahead of time, e.g. offline transaction signing)")
	RootCmd.AddCommand(accountCmd)
	RootCmd.AddCommand(sendCmd)
	RootCmd.AddCommand(claimFaucetCmd)
	RootCmd.AddCommand(genFaucetCmd)
	RootCmd.AddCommand(mintCmd)
	RootCmd.AddCommand(keysCmd)
	keysCmd.AddCommand(keysNewCmd)
	keysCmd.AddCommand(keysImportCmd)
	keysCmd.AddCommand(keysListCmd)
	keysCmd.AddCommand(keysChangePasswordCmd)

	keysNewCmd.Flags().StringVarP(&infoUri, "info-uri", "i", "ipfs://", "Set the Account's InfoURI")
}

func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(1)
}

var RootCmd = &cobra.Command{
	Use:   "vocli",
	Short: "vocli is a convenience CLI that helps you do things on Vochain",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if debug {
			log.Init("debug", "stdout")
		} else {
			log.Init("error", "stdout")
		}
	},
}

var sendCmd = &cobra.Command{
	Use:   "send <from keystore, recipient, amount>",
	Short: "Send tokens to another account",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("sorry, what amount did you say again? %s", err)
		}

		_, signer, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}
		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}

		nonce, err := getNonce(c, signer.AddressString())
		if err != nil {
			return fmt.Errorf("could not lookup the account's nonce, try specifying manually: %s", err)
		}

		err = c.SendTokens(signer, common.HexToAddress(args[1]), amount, *nonce)
		return err
	},
}

var claimFaucetCmd = &cobra.Command{
	Use:   "claimfaucet <to keystore, hex encoded faucet package>",
	Short: "Claim tokens from another account, using a payload generated from that account that acts as an authorization.",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		faucetPackageRaw, err := hex.DecodeString(args[1])
		if err != nil {
			return fmt.Errorf("could not decode the hex-encoded input: %s", err)
		}

		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}
		_, signer, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return err
		}

		faucetPackage := &models.FaucetPackage{}
		err = proto.Unmarshal(faucetPackageRaw, faucetPackage)
		if err != nil {
			return fmt.Errorf("could not unmarshal the faucet package: %s", err)
		}

		err = c.CollectFaucet(signer, faucetPackage)
		if err != nil {
			return err
		}
		return nil
	},
}

var genFaucetCmd = &cobra.Command{
	Use:   "genfaucet <from keystore, recipient, amount>",
	Short: "Generate a payload allowing another account to claim tokens from this account.",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, signer, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}
		amount, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("sorry, what amount did you say again? %s", err)
		}

		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}

		faucetPackage, err := c.GenerateFaucetPackage(signer, common.HexToAddress(args[1]), amount)
		if err != nil {
			return err
		}

		faucetPackageMarshaled, err := proto.Marshal(faucetPackage)
		if err != nil {
			return err
		}
		fmt.Fprintln(Stdout, hex.EncodeToString(faucetPackageMarshaled))
		return nil
	},
}

var mintCmd = &cobra.Command{
	Use:   "mint <treasurer's keystore, recipient, amount>",
	Short: "Mint more tokens to an address. Only the Treasurer may do this.",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		amount, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("sorry, what amount did you say again? %s", err)
		}
		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}

		_, signer, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}

		nonce, err := getNonce(c, signer.AddressString())
		if err != nil {
			return fmt.Errorf("could not lookup the account's nonce, try specifying manually: %s", err)
		}

		err = c.MintTokens(signer, common.HexToAddress(args[1]), amount, *nonce)
		return err
	},
}

var accountCmd = &cobra.Command{
	Use:   "info",
	Short: "Get information about an account. The address may or may not include the 0x prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}
		resp, err := c.GetAccount(nil, common.HexToAddress(args[0]))
		if err != nil {
			return err
		}
		fmt.Fprintln(Stdout, "resp", resp.String(), "err", err)
		return err
	},
}

func getNonce(c *client.Client, address string) (*uint32, error) {
	resp, err := c.GetAccount(nil, common.HexToAddress(address))
	if err != nil {
		return nil, err
	}
	return &resp.Account.Nonce, nil
}

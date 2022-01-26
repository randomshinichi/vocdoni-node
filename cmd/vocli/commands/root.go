package commands

import (
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"strconv"

	"github.com/ethereum/go-ethereum/common"
	"github.com/spf13/cobra"
	"go.vocdoni.io/dvote/api"
	"go.vocdoni.io/dvote/client"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/dvote/log"
	"go.vocdoni.io/proto/build/go/models"
	"google.golang.org/protobuf/proto"
)

var gatewayRpc string
var debug bool

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.PersistentFlags().StringVarP(&gatewayRpc, "url", "u", "https://gw1.dev.vocdoni.net/dvote", "Gateway RPC URL")
	rootCmd.PersistentFlags().BoolVarP(&debug, "debug", "d", false, "prints additional information")
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
	Use:   "send <from keystore, recipient, amount>",
	Short: "Send tokens to another account",
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

		key, err := openKeyfile(args[0], "Please unlock your key")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}

		nonce, err := getNonce(c, key.Address.Hex())
		if err != nil {
			return fmt.Errorf("could not lookup the account's nonce, try specifying manually: %s", err)
		}

		tx := &models.Tx{
			Payload: &models.Tx_SendTokens{SendTokens: &models.SendTokensTx{
				Txtype: models.TxType_SEND_TOKENS,
				Nonce:  *nonce,
				From:   key.Address.Bytes(),
				To:     common.HexToAddress(args[1]).Bytes(),
				Value:  amount,
			}}}

		signer := ethereum.NewSignKeys()
		signer.Private = *key.PrivateKey
		stx, err := signTx(tx, signer)
		if err != nil {
			return err
		}

		resp, err := submitRawTx(stx, c)
		if err != nil {
			return err
		}
		fmt.Println("resp", resp)
		return nil
	},
}

var claimFaucetCmd = &cobra.Command{
	Use:   "claimfaucet <hex encoded faucet package>",
	Short: "Claim tokens from another account, using a payload generated from that account that acts as an authorization.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		faucetPackageRaw, err := hex.DecodeString(args[0])
		if err != nil {
			return fmt.Errorf("could not decode the hex-encoded input: %s", err)
		}
		c, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}

		faucetPackage := &models.FaucetPackage{}
		err = proto.Unmarshal(faucetPackageRaw, faucetPackage)
		if err != nil {
			return fmt.Errorf("could not unmarshal the faucet package: %s", err)
		}

		tx := &models.Tx{
			Payload: &models.Tx_CollectFaucet{
				CollectFaucet: &models.CollectFaucetTx{
					TxType:        models.TxType_COLLECT_FAUCET,
					FaucetPackage: faucetPackage,
				},
			},
		}

		fmt.Println("Sending Transaction")
		req := &api.APIrequest{}
		req.Method = "submitRawTx"
		req.Payload, err = proto.Marshal(tx)
		if err != nil {
			return fmt.Errorf("could not marshal transaction: %v", err)
		}
		resp, err := c.Request(*req, nil)
		if err != nil {
			return err
		}

		fmt.Println("resp", resp)
		return nil
	},
}

var genFaucetCmd = &cobra.Command{
	Use:   "genfaucet <from keystore, recipient, amount>",
	Short: "Generate a payload allowing another account to claim tokens from this account.",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, err := openKeyfile(args[0], "Please unlock your key")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}
		amount, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return fmt.Errorf("sorry, what amount did you say again? %s", err)
		}

		faucetPackage := &models.FaucetPackage{
			Payload: &models.FaucetPayload{
				Identifier: rand.Uint64(),
				To:         common.HexToAddress(args[1]).Bytes(),
				Amount:     amount,
			},
			Signature: []byte{},
		}
		log.Debug(faucetPackage)

		signer := ethereum.NewSignKeys()
		signer.Private = *key.PrivateKey
		faucetPayloadRaw, err := proto.Marshal(faucetPackage.Payload)
		if err != nil {
			return fmt.Errorf("could not marshal the faucet payload for signing: %s", err)
		}
		faucetPackage.Signature, err = signer.Sign(faucetPayloadRaw)
		if err != nil {
			return err
		}

		faucetPackageRaw, err := proto.Marshal(faucetPackage)
		if err != nil {
			return err
		}
		fmt.Println("Faucet Package", hex.EncodeToString(faucetPackageRaw))
		fmt.Println("Faucet Package's Signature", hex.EncodeToString(faucetPackage.Signature))
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

		key, err := openKeyfile(args[0], "Please unlock your key")
		if err != nil {
			return fmt.Errorf("could not open keyfile %s", err)
		}

		nonce, err := getNonce(c, key.Address.Hex())
		if err != nil {
			return fmt.Errorf("could not lookup the account's nonce, try specifying manually: %s", err)
		}

		tx := &models.Tx{
			Payload: &models.Tx_MintTokens{MintTokens: &models.MintTokensTx{
				Txtype: models.TxType_MINT_TOKENS,
				Nonce:  *nonce,
				To:     common.HexToAddress(args[1]).Bytes(),
				Value:  amount,
			}}}
		signer := ethereum.NewSignKeys()
		signer.Private = *key.PrivateKey
		stx, err := signTx(tx, signer)
		if err != nil {
			return err
		}

		resp, err := submitRawTx(stx, c)
		if err != nil {
			return err
		}
		fmt.Println("resp", resp)
		return nil
	},
}

var accountCmd = &cobra.Command{
	Use:   "info",
	Short: "Get information about an account. The address may or may not include the 0x prefix",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := client.New(gatewayRpc)
		if err != nil {
			return err
		}
		resp, err := getAccount(client, args[0])
		if err != nil {
			return err
		}
		fmt.Println("resp", resp.String(), "err", err)
		return err
	},
}

func getAccount(c *client.Client, address string) (*api.APIresponse, error) {
	req := &api.APIrequest{}
	req.EntityId = common.HexToAddress(address).Bytes()
	req.Method = "getAccount"
	resp, err := c.Request(*req, nil)
	if err != nil {
		return nil, err
	} else if resp.Nonce == nil {
		return resp, fmt.Errorf("account %s does not exist", address)
	}
	return resp, nil
}

func getNonce(c *client.Client, address string) (*uint32, error) {
	resp, err := getAccount(c, address)
	return resp.Nonce, err
}

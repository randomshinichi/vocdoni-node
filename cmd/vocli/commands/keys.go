package commands

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"go.vocdoni.io/dvote/client"
	"go.vocdoni.io/dvote/crypto/ethereum"
	"go.vocdoni.io/proto/build/go/models"
	"golang.org/x/term"
)

const (
	scryptN = ethkeystore.StandardScryptN
	scryptP = ethkeystore.StandardScryptP
	keyExt  = ".vokey"
)

var keysCmd = &cobra.Command{
	Use:   "keys",
	Short: "Do things related to keys",
	Long:  "In Vochain, we make the distinction between a key and an account. A Key controls an Account which is stored in the blockchain's state. The Key itself is stored in encrypted form on the user's disk.",
}

var keysNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Generate a new key and create its corresponding Account on the chain.",
	Args:  cobra.ExactArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Printf("Step 1/2: First we will generate the key and save it on your disk, encrypted.\nUnlike other blockchains, an Account for this key must first be created on the blockchain for others to send you funds (or any other operations).\nOnce the key is generated and saved, we will send a SetAccountInfo transaction to the blockchain in order to create an Account for this key. \nFear not, if you have only generated a key, you may skip the key generation process and directly send the SetAccountInfo transaction by re-running 'keys new <full path to keyfile>'\n\n")

		password, err := PromptPassword("Your new key file will be locked with a password. Please give a password: ")
		if err != nil {
			return err
		}

		key, keyPath, err := storeNewKey(rand.Reader, password)
		if err != nil {
			return fmt.Errorf("couldn't generate a new key", err)
		}
		fmt.Printf("\nYour new key was generated\n")
		fmt.Printf("Public address of the key:   %s\n", key.Address.Hex())
		fmt.Printf("Path of the secret key file: %s\n", keyPath)
		fmt.Printf("- As usual, please BACKUP your key file and REMEMBER your password!\n")

		fmt.Printf("\nStep 2/2: Sending SetAccountInfo to create an Account for key %s on %s\n", key.Address.String(), gatewayRpc)
		tx := &models.Tx{
			Payload: &models.Tx_SetAccountInfo{
				SetAccountInfo: &models.SetAccountInfoTx{
					Txtype:  models.TxType_SET_ACCOUNT_INFO,
					Nonce:   0,
					InfoURI: "ipfs://",
					Account: key.Address.Bytes(),
				},
			}}

		signer := ethereum.NewSignKeys()
		signer.Private = *key.PrivateKey
		stx, err := signTx(tx, signer)
		if err != nil {
			return err
		}

		c, err := client.New(gatewayRpc)
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

var keysImportCmd = &cobra.Command{
	Use:   "import <keyfile>",
	Short: "Reads a plain, unencrypted file containing a private key (hexstring) and stores it in Ethereum's encrypted JSON format.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		keyfile := args[0]
		key, err := crypto.LoadECDSA(keyfile)
		if err != nil {
			utils.Fatalf("Failed to load the private key: %v", err)
		}
		passphrase, err := PromptPassword("Your new account is locked with a password. Please give a password.")
		if err != nil {
			return err
		}
		id, err := uuid.NewRandom()
		if err != nil {
			panic(fmt.Sprintf("Could not create random uuid: %v", err))
		}
		k := &ethkeystore.Key{
			Id:         id,
			Address:    crypto.PubkeyToAddress(key.PublicKey),
			PrivateKey: key,
		}

		keyjson, err := ethkeystore.EncryptKey(k, passphrase, scryptN, scryptP)
		if err != nil {
			return err
		}

		keyPath, err := generateKeyFilename(k.Address)
		if err != nil {
			return err
		}
		err = writeKeyFile(keyPath, keyjson)
		if err != nil {
			return err
		}

		fmt.Printf("Your imported key is stored at ", keyPath)
		return nil
	},
}

var keysListCmd = &cobra.Command{
	Use:   "list",
	Short: "Lists the keys that vocli knows about.",
	Args:  cobra.ExactArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		keysDir, err := getKeysDir()
		if err != nil {
			return err
		}
		return filepath.Walk(keysDir, func(path string, info os.FileInfo, err error) error {
			if !info.IsDir() && (filepath.Ext(path) == keyExt) {
				fmt.Println(path)
			}
			return nil
		})
	},
}

var keysChangePasswordCmd = &cobra.Command{
	Use:   "changepassword <filename>",
	Short: "Changes the password of a keyfile.",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		k, err := openKeyfile(args[0], "Enter old password")
		if err != nil {
			return err
		}

		passwordNew, err := PromptPassword("Enter new password:")
		if err != nil {
			return err
		}
		keyJSONNew, err := ethkeystore.EncryptKey(k, passwordNew, scryptN, scryptP)
		if err != nil {
			return fmt.Errorf("couldn't encrypt the key with new password: %s", err)
		}

		return writeKeyFile(args[0], keyJSONNew)
	},
}

func openKeyfile(path, prompt string) (*ethkeystore.Key, error) {
	keyJSON, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	password, err := PromptPassword(prompt)
	if err != nil {
		return nil, err
	}
	k, err := ethkeystore.DecryptKey(keyJSON, string(password))
	if err != nil {
		return nil, fmt.Errorf("couldn't decrypt the key with given password: %s", err)
	}
	return k, nil
}

func PromptPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	password, err := term.ReadPassword(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	fmt.Print("\n")
	return string(password), err
}

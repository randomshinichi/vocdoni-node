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
	Short: "Generate a new key that lets you sign transactions to control an Account",
	Args:  cobra.ExactArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		password := utils.GetPassPhraseWithList("Your new account is locked with a password. Please give a password. Do not forget this password.", true, 0, nil)

		key, keyPath, err := storeNewKey(rand.Reader, password)
		if err != nil {
			return fmt.Errorf("couldn't generate a new key", err)
		}
		fmt.Printf("\nYour new key was generated\n\n")
		fmt.Printf("Public address of the key:   %s\n", key.Address.Hex())
		fmt.Printf("Path of the secret key file: %s\n\n", keyPath)
		fmt.Printf("- You can share your public address with anyone. Others need it to interact with you.\n")
		fmt.Printf("- You must NEVER share the secret key with anyone! The key controls access to your funds!\n")
		fmt.Printf("- You must BACKUP your key file! Without the key, it's impossible to access account funds!\n")
		fmt.Printf("- You must REMEMBER your password! Without the password, it's impossible to decrypt the key!\n\n")
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
		passphrase := utils.GetPassPhraseWithList("Your new account is locked with a password. Please give a password. Do not forget this password.", true, 0, nil)
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
		k, err := openKeyfile(args[0], "Enter old password:")
		if err != nil {
			return err
		}

		passwordNew := utils.GetPassPhraseWithList("Enter new password:", false, 0, nil)
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
	password := utils.GetPassPhraseWithList(prompt, false, 0, nil)
	k, err := ethkeystore.DecryptKey(keyJSON, password)
	if err != nil {
		return nil, fmt.Errorf("couldn't decrypt the key with given password: %s", err)
	}
	return k, nil
}

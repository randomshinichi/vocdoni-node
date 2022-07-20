package commands

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"go.vocdoni.io/dvote/client"
	"go.vocdoni.io/proto/build/go/models"
)

var processCmd = &cobra.Command{
	Use:   "process",
	Short: "Create/update/end a process",
}

var setProcessCmd = &cobra.Command{
	Use:   "set <keystore> <process id> <process status>",
	Short: "Set a Process's status, where status is one of PROCESS_UNKNOWN,READY,ENDED,CANCELED,PAUSED,RESULTS",
	Args: func(cmd *cobra.Command, args []string) error {
		if len(args) < 3 {
			return fmt.Errorf("requires <keystore> <process id> <process status>")
		}
		if _, exists := models.ProcessStatus_value[args[2]]; !exists {
			return fmt.Errorf("invalid process status specified")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.New(v.GetString(urlKey))
		if err != nil {
			return err
		}
		_, key, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return err
		}
		pid, err := hex.DecodeString(args[1])
		if err != nil {
			return fmt.Errorf("could not decode hexstring %s into bytes", args[1])
		}
		return c.SetProcessStatus(key, pid, args[2])
	},
}

var newProcessCmd = &cobra.Command{
	Use:   "new <keystore> <censusRoot> <censusURI>",
	Short: "Create a new Process",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		c, err := client.New(v.GetString(urlKey))
		if err != nil {
			return err
		}
		_, key, err := openKeyfile(args[0], "Please unlock your key: ")
		if err != nil {
			return err
		}
		censusRootBytes, err := hex.DecodeString(args[1])
		if err != nil {
			return fmt.Errorf("could not decode hexstring %s into bytes", args[1])
		}

		e, err := cmd.Flags().GetString("envelope-options")
		if err != nil {
			return err
		}
		envelopeType, err := parseEnvelopeArgs(e)
		if err != nil {
			return err
		}

		p, err := cmd.Flags().GetString("process-mode")
		if err != nil {
			return err
		}
		processMode, err := parseProcessModeArgs(p)
		if err != nil {
			return err
		}

		censusOriginFlag, err := cmd.Flags().GetString("census-origin")
		if err != nil {
			return err
		}
		censusOrigin, exists := models.CensusOrigin_value[censusOriginFlag]
		if !exists {
			return fmt.Errorf("invalid census origin specified")
		}

		maxCensusSize, err := cmd.Flags().GetUint64("max-census-size")
		if err != nil {
			return err
		}
		startBlockFlag, err := cmd.Flags().GetInt("start-block")
		if err != nil {
			return err
		}
		if startBlockFlag == 0 {
			h, err := c.GetCurrentBlock()
			if err != nil {
				return fmt.Errorf("could not get current height: %s", err)
			}
			startBlockFlag = int(h)
		}
		durationFlag, err := cmd.Flags().GetInt("duration")
		if err != nil {
			return err
		}

		startBlock, processID, err := c.CreateProcess(key, key.Address().Bytes(), censusRootBytes, args[2], &envelopeType, &processMode, models.CensusOrigin(censusOrigin), startBlockFlag, durationFlag, maxCensusSize)
		if err != nil {
			return fmt.Errorf("could not create process: %s", err)
		}
		fmt.Fprintf(Stdout, "Process %s will start on block %d\n", hex.EncodeToString(processID), startBlock)

		time.Sleep(4 * time.Second)
		process, err := c.GetProcessInfo(processID)
		if err != nil {
			return err
		}
		fmt.Fprintln(Stdout, process)
		return nil
	},
}

func contains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}
func parseEnvelopeArgs(i string) (models.EnvelopeType, error) {
	allowed := []string{"serial", "anonymous", "encryptedvotes", "uniquevalues", "costfromweight", ""}
	et := models.EnvelopeType{}
	options := strings.Split(i, ",")
	for _, o := range options {
		if !contains(allowed, o) {
			return et, fmt.Errorf("envelope-type may only contain %v, not %s", allowed, o)
		}
		switch o {
		case "serial":
			et.Serial = true
		case "anonymous":
			et.Anonymous = true
		case "encryptedvotes":
			et.EncryptedVotes = true
		case "uniquevalues":
			et.UniqueValues = true
		case "costfromweight":
			et.CostFromWeight = true
		}
	}
	return et, nil
}

func parseProcessModeArgs(i string) (models.ProcessMode, error) {
	allowed := []string{"autostart", "interruptible", "dynamiccensus", "encryptedmetadata", "preregister", ""}
	p := models.ProcessMode{}
	options := strings.Split(i, ",")
	for _, o := range options {
		if !contains(allowed, o) {
			return p, fmt.Errorf("process-mode may only contain %v, not %s", allowed, o)
		}
		switch o {
		case "autostart":
			p.AutoStart = true
		case "interruptible":
			p.Interruptible = true
		case "dynamiccensus":
			p.DynamicCensus = true
		case "encryptedmetadata":
			p.EncryptedMetaData = true
		case "preregister":
			p.PreRegister = true
		}
	}
	return p, nil
}

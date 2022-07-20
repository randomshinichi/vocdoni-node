package commands

import (
	"reflect"
	"testing"

	"go.vocdoni.io/proto/build/go/models"
)

type envelopeTestCase struct {
	input      string
	want       models.EnvelopeType
	expectFail bool
}

func TestParseEnvelopeArgs(t *testing.T) {
	tc := []envelopeTestCase{
		{
			input: "",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: false,
			},
			expectFail: false,
		},
		{
			input: "serial",
			want: models.EnvelopeType{
				Serial:         true,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: false,
			},
			expectFail: false,
		},
		{
			input: "anonymous",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      true,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: false,
			},
			expectFail: false,
		},
		{
			input: "encryptedvotes",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      false,
				EncryptedVotes: true,
				UniqueValues:   false,
				CostFromWeight: false,
			},
			expectFail: false,
		},
		{
			input: "uniquevalues",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   true,
				CostFromWeight: false,
			},
			expectFail: false,
		},
		{
			input: "costfromweight",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: true,
			},
			expectFail: false,
		},
		{
			input: "costfromweight,serial",
			want: models.EnvelopeType{
				Serial:         true,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: true,
			},
			expectFail: false,
		},
		{
			input: "adfds|serial,erwe",
			want: models.EnvelopeType{
				Serial:         false,
				Anonymous:      false,
				EncryptedVotes: false,
				UniqueValues:   false,
				CostFromWeight: false,
			},
			expectFail: true,
		},
	}
	for _, testCase := range tc {
		a, err := parseEnvelopeArgs(testCase.input)
		if err != nil && !testCase.expectFail {
			t.Error(err)
		}
		if !reflect.DeepEqual(a, testCase.want) {
			t.Errorf("%#v did not equal expected value %#v", a, testCase.want)
		}
	}
}

type processModeTestCase struct {
	input      string
	want       models.ProcessMode
	expectFail bool
}

func TestParseProcessModeArgs(t *testing.T) {
	tc := []processModeTestCase{
		{
			input: "",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       false,
			},
			expectFail: false,
		},
		{
			input: "autostart",
			want: models.ProcessMode{
				AutoStart:         true,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       false,
			},
			expectFail: false,
		},
		{
			input: "interruptible",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     true,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       false,
			},
			expectFail: false,
		},
		{
			input: "dynamiccensus",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     false,
				DynamicCensus:     true,
				EncryptedMetaData: false,
				PreRegister:       false,
			},
			expectFail: false,
		},
		{
			input: "encryptedmetadata",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: true,
				PreRegister:       false,
			},
			expectFail: false,
		},
		{
			input: "preregister",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       true,
			},
			expectFail: false,
		},
		{
			input: "autostart,preregister",
			want: models.ProcessMode{
				AutoStart:         true,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       true,
			},
			expectFail: false,
		},
		{
			input: "adfds|encryptedvotes,erwe",
			want: models.ProcessMode{
				AutoStart:         false,
				Interruptible:     false,
				DynamicCensus:     false,
				EncryptedMetaData: false,
				PreRegister:       false,
			},
			expectFail: true,
		},
	}
	for _, testCase := range tc {
		a, err := parseProcessModeArgs(testCase.input)
		if err != nil && !testCase.expectFail {
			t.Error(err)
		}
		if !reflect.DeepEqual(a, testCase.want) {
			t.Errorf("%#v did not equal expected value %#v", a, testCase.want)
		}
	}
}

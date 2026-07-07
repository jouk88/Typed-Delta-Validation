/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package kvledger

import (
	"testing"

	"github.com/hyperledger/fabric/common/capabilities"
	"github.com/hyperledger/fabric/core/config/configtest"
	"github.com/hyperledger/fabric/core/ledger/mock"
	"github.com/hyperledger/fabric/internal/configtxgen/encoder"
	"github.com/hyperledger/fabric/internal/configtxgen/genesisconfig"
	"github.com/stretchr/testify/require"
)

func TestTypedDeltaCapabilityFromGenesisOnCreatePath(t *testing.T) {
	conf, cleanup := testConfig(t)
	defer cleanup()
	provider := testutilNewProvider(conf, t, &mock.DeployedChaincodeInfoProvider{})
	defer provider.Close()

	confOn := genesisconfig.Load(genesisconfig.SampleDevModeSoloProfile, configtest.GetDevConfigDir())
	require.NotNil(t, confOn.Application, "sample profile must have an application section")
	if confOn.Application.Capabilities == nil {
		confOn.Application.Capabilities = map[string]bool{}
	}
	confOn.Application.Capabilities[capabilities.ApplicationTypedDelta] = true
	gbOn := encoder.New(confOn).GenesisBlockForChannel("td-cap-on")

	confOff := genesisconfig.Load(genesisconfig.SampleDevModeSoloProfile, configtest.GetDevConfigDir())
	if confOff.Application.Capabilities != nil {
		delete(confOff.Application.Capabilities, capabilities.ApplicationTypedDelta)
	}
	gbOff := encoder.New(confOff).GenesisBlockForChannel("td-cap-off")

	emptyStore, err := provider.blkStoreProvider.Open("td-cap-empty")
	require.NoError(t, err)
	defer emptyStore.Shutdown()

	bcInfo, err := emptyStore.GetBlockchainInfo()
	require.NoError(t, err)
	require.Equal(t, uint64(0), bcInfo.Height, "precondition: empty store mirrors pre-genesis-commit create path")

	require.True(t, typedDeltaCapabilityEnabled(emptyStore, gbOn),
		"capability-on genesis must enable typed-delta on the create path (Height==0) without restart")
	require.False(t, typedDeltaCapabilityEnabled(emptyStore, gbOff),
		"capability-off genesis must stay disabled")
	require.False(t, typedDeltaCapabilityEnabled(emptyStore, nil),
		"empty store with no genesis must fail-safe to disabled")
}

package api

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
	"path/filepath"

	"github.com/NebulousLabs/Sia/build"
	"github.com/NebulousLabs/Sia/crypto"
	"github.com/NebulousLabs/Sia/modules"
	"github.com/NebulousLabs/Sia/modules/consensus"
	"github.com/NebulousLabs/Sia/modules/explorer"
	"github.com/NebulousLabs/Sia/modules/gateway"
	"github.com/NebulousLabs/Sia/modules/host"
	"github.com/NebulousLabs/Sia/modules/hostdb"
	"github.com/NebulousLabs/Sia/modules/miner"
	"github.com/NebulousLabs/Sia/modules/renter"
	"github.com/NebulousLabs/Sia/modules/transactionpool"
	"github.com/NebulousLabs/Sia/modules/wallet"
	"github.com/NebulousLabs/Sia/types"
)

// serverTester contains a server and a set of channels for keeping all of the
// modules synchronized during testing.
type serverTester struct {
	cs        modules.ConsensusSet
	gateway   modules.Gateway
	host      modules.Host
	hostdb    modules.HostDB
	miner     modules.TestMiner
	renter    modules.Renter
	tpool     modules.TransactionPool
	exp       modules.Explorer
	wallet    modules.Wallet
	walletKey crypto.TwofishKey

	server *Server
}

// createServerTester creates a server tester object that is ready for testing,
// including money in the wallet and all modules initalized.
func createServerTester(name string) (*serverTester, error) {
	// Create the testing directory.
	testdir := build.TempDir("api", name)

	// Create the modules.
	g, err := gateway.New(":0", filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, err := consensus.New(g, filepath.Join(testdir, modules.ConsensusDir))
	if err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g)
	if err != nil {
		return nil, err
	}
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}
	key, err := crypto.GenerateTwofishKey()
	if err != nil {
		return nil, err
	}
	_, err = w.Encrypt(key)
	if err != nil {
		return nil, err
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}
	hdb, err := hostdb.New(cs, g, filepath.Join(testdir, modules.HostDBDir))
	if err != nil {
		return nil, err
	}
	h, err := host.New(cs, hdb, tp, w, ":0", filepath.Join(testdir, modules.HostDir))
	if err != nil {
		return nil, err
	}
	r, err := renter.New(cs, hdb, w, tp, filepath.Join(testdir, modules.RenterDir))
	if err != nil {
		return nil, err
	}
	exp, err := explorer.New(cs, filepath.Join(testdir, modules.ExplorerDir))
	if err != nil {
		return nil, err
	}
	srv, err := NewServer(":0", cs, g, h, hdb, m, r, tp, w, exp)
	if err != nil {
		return nil, err
	}

	// Assemble the serverTester.
	st := &serverTester{
		cs:        cs,
		gateway:   g,
		host:      h,
		hostdb:    hdb,
		miner:     m,
		renter:    r,
		tpool:     tp,
		exp:       exp,
		wallet:    w,
		walletKey: key,

		server: srv,
	}

	// TODO: A more reasonable way of listening for server errors.
	go func() {
		listenErr := srv.Serve()
		if listenErr != nil {
			panic(listenErr)
		}
	}()

	// Mine blocks until the wallet has confirmed money.
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		_, err := st.miner.AddBlock()
		if err != nil {
			return nil, err
		}
	}

	return st, nil
}

// netAddress returns the NetAddress of the caller.
func (st *serverTester) netAddress() modules.NetAddress {
	return st.server.gateway.Address()
}

// coinAddress returns a coin address that the caller is able to spend from.
func (st *serverTester) coinAddress() string {
	var addr struct {
		Address string
	}
	st.getAPI("/wallet/address", &addr)
	return addr.Address
}

// getAPI makes an API call and decodes the response.
func (st *serverTester) getAPI(call string, obj interface{}) error {
	resp, err := HttpGET("http://" + st.server.listener.Addr().String() + call)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a call error.
	if resp.StatusCode != http.StatusOK {
		respErr, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(respErr))
	}

	// Decode the response into 'obj'.
	err = json.NewDecoder(resp.Body).Decode(obj)
	if err != nil {
		return err
	}
	return nil
}

// postAPI makes an API call and decodes the response.
func (st *serverTester) postAPI(call string, values url.Values, obj interface{}) error {
	resp, err := HttpPOST("http://"+st.server.listener.Addr().String()+call, values.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a call error.
	if resp.StatusCode != http.StatusOK {
		respErr, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(respErr))
	}

	// Decode the response into 'obj'.
	err = json.NewDecoder(resp.Body).Decode(obj)
	if err != nil {
		return err
	}
	return nil
}

// stdGetAPI makes an API call and discards the response.
func (st *serverTester) stdGetAPI(call string) error {
	resp, err := HttpGET("http://" + st.server.listener.Addr().String() + call)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a call error.
	if resp.StatusCode != http.StatusOK {
		respErr, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(respErr))
	}
	return nil
}

// stdPostAPI makes an API call and discards the response.
func (st *serverTester) stdPostAPI(call string, values url.Values) error {
	resp, err := HttpPOST("http://"+st.server.listener.Addr().String()+call, values.Encode())
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Check for a call error.
	if resp.StatusCode != http.StatusOK {
		respErr, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		return errors.New(string(respErr))
	}
	return nil
}

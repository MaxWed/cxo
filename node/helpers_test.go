package node

import (
	"errors"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	//"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/cxo/node/gnet"
)

// timeout to fail slow opertions
const TM time.Duration = 50 * time.Millisecond

var (
	testDataDir      = filepath.Join(".", "test")
	testServerDBPath = filepath.Join(testDataDir, "server.db")
)

func init() {
	if !testing.Verbose() {
		log.SetOutput(ioutil.Discard) // for RPC logs
	}
}

func clean() {
	os.RemoveAll(testDataDir)
}

func shouldPanic(t *testing.T) {
	if recover() == nil {
		t.Error("missing panic")
	}
}

// name for logs (empty for default)
// memory - to use databas in memory (otherwise it will be ./test/test.db)
// listening enabled by argument
func newConfig(listen bool) (conf Config) {
	conf = NewConfig()
	conf.Log.Debug = testing.Verbose()
	if !testing.Verbose() {
		conf.Log.Output = ioutil.Discard
	}
	conf.Listen = "127.0.0.1:0" // arbitrary assignment
	conf.EnableListener = listen

	conf.EnableRPC = false
	conf.RPCAddress = "127.0.0.1:0" // arbitrary assignment

	conf.InMemoryDB = true
	conf.DataDir = testDataDir
	conf.DBPath = testServerDBPath
	return
}

// b - listener (listens anyway)
// a - connects to b (can listen and can not)
func newConnectedNodes(aconf, bconf Config) (a, b *Node,
	ac, bc *gnet.Conn, err error) {

	bconf.EnableListener = true

	// accept connection by b
	accept := make(chan *gnet.Conn, 1)

	var onCreateConnection = func(c *gnet.Conn) {
		select {
		case accept <- c: // never block here
		default:
		}
	}

	if cc := bconf.Config.OnCreateConnection; cc == nil {
		bconf.Config.OnCreateConnection = onCreateConnection
	} else {
		bconf.Config.OnCreateConnection = func(c *gnet.Conn) {
			cc(c)
			onCreateConnection(c)
		}
	}

	if a, err = NewNode(aconf); err != nil {
		return
	}

	if b, err = NewNode(bconf); err != nil {
		a.Close()
		return
	}

	if ac, err = a.Pool().Dial(b.Pool().Address()); err != nil {
		a.Close()
		b.Close()
		return
	}
	// dialing prefoms asynchronously and we need to wait until
	// connection of b will be created
	select {
	case bc = <-accept:
		if bc == nil {
			err = errors.New("misisng connection")
		}
	case <-time.After(TM):
		a.Close()
		b.Close()
		err = errors.New("slow")
		return
	}
	return
}

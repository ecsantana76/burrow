package lib

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/hyperledger/burrow/process"

	"github.com/hyperledger/burrow/logging/lifecycle"
	"github.com/hyperledger/burrow/rpc/lib/client"
	"github.com/hyperledger/burrow/rpc/lib/server"
	"github.com/hyperledger/burrow/rpc/lib/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cmn "github.com/tendermint/tendermint/libs/common"
	"github.com/tendermint/tendermint/libs/log"
)

// Client and Server should work over tcp or unix sockets
const (
	tcpAddr = "tcp://0.0.0.0:47768"

	unixSocket = "/tmp/rpc_test.sock"
	unixAddr   = "unix://" + unixSocket

	websocketEndpoint = "/websocket/endpoint"
)

type ResultEcho struct {
	Value string `json:"value"`
}

type ResultEchoInt struct {
	Value int `json:"value"`
}

type ResultEchoBytes struct {
	Value []byte `json:"value"`
}

type ResultEchoDataBytes struct {
	Value cmn.HexBytes `json:"value"`
}

// Define some routes
var Routes = map[string]*server.RPCFunc{
	"echo":            server.NewRPCFunc(EchoResult, "arg"),
	"echo_ws":         server.NewWSRPCFunc(EchoWSResult, "arg"),
	"echo_bytes":      server.NewRPCFunc(EchoBytesResult, "arg"),
	"echo_data_bytes": server.NewRPCFunc(EchoDataBytesResult, "arg"),
	"echo_int":        server.NewRPCFunc(EchoIntResult, "arg"),
}

func EchoResult(v string) (*ResultEcho, error) {
	return &ResultEcho{v}, nil
}

func EchoWSResult(wsCtx types.WSRPCContext, v string) (*ResultEcho, error) {
	return &ResultEcho{v}, nil
}

func EchoIntResult(v int) (*ResultEchoInt, error) {
	return &ResultEchoInt{v}, nil
}

func EchoBytesResult(v []byte) (*ResultEchoBytes, error) {
	return &ResultEchoBytes{v}, nil
}

func EchoDataBytesResult(v cmn.HexBytes) (*ResultEchoDataBytes, error) {
	return &ResultEchoDataBytes{v}, nil
}

func TestMain(m *testing.M) {
	setup()
	code := m.Run()
	os.Exit(code)
}

// launch unix and tcp servers
func setup() {
	logger, _ := lifecycle.NewStdErrLogger()

	cmd := exec.Command("rm", "-f", unixSocket)
	err := cmd.Start()
	if err != nil {
		panic(err)
	}
	if err = cmd.Wait(); err != nil {
		panic(err)
	}

	tcpLogger := logger.With("socket", "tcp")
	mux := http.NewServeMux()
	server.RegisterRPCFuncs(mux, Routes, tcpLogger)
	wm := server.NewWebsocketManager(Routes, logger, server.ReadWait(5*time.Second), server.PingPeriod(1*time.Second))
	mux.HandleFunc(websocketEndpoint, wm.WebsocketHandler)
	go func() {
		l, err := process.ListenerFromAddress(tcpAddr)
		if err != nil {
			panic(err)
		}
		_, err = server.StartHTTPServer(l, mux, tcpLogger)
		if err != nil {
			panic(err)
		}
	}()

	unixLogger := logger.With("socket", "unix")
	mux2 := http.NewServeMux()
	server.RegisterRPCFuncs(mux2, Routes, unixLogger)
	wm = server.NewWebsocketManager(Routes, logger)
	mux2.HandleFunc(websocketEndpoint, wm.WebsocketHandler)
	go func() {
		l, err := process.ListenerFromAddress(unixAddr)
		if err != nil {
			panic(err)
		}
		_, err = server.StartHTTPServer(l, mux2, unixLogger)
		if err != nil {
			panic(err)
		}
	}()

	// wait for servers to start
	time.Sleep(time.Second * 2)
}

func echoViaHTTP(cl client.HTTPClient, val string) (string, error) {
	params := map[string]interface{}{
		"arg": val,
	}
	result := new(ResultEcho)
	if _, err := cl.Call("echo", params, result); err != nil {
		return "", err
	}
	return result.Value, nil
}

func echoIntViaHTTP(cl client.HTTPClient, val int) (int, error) {
	params := map[string]interface{}{
		"arg": val,
	}
	result := new(ResultEchoInt)
	if _, err := cl.Call("echo_int", params, result); err != nil {
		return 0, err
	}
	return result.Value, nil
}

func echoBytesViaHTTP(cl client.HTTPClient, bytes []byte) ([]byte, error) {
	params := map[string]interface{}{
		"arg": bytes,
	}
	result := new(ResultEchoBytes)
	if _, err := cl.Call("echo_bytes", params, result); err != nil {
		return []byte{}, err
	}
	return result.Value, nil
}

func echoDataBytesViaHTTP(cl client.HTTPClient, bytes cmn.HexBytes) (cmn.HexBytes, error) {
	params := map[string]interface{}{
		"arg": bytes,
	}
	result := new(ResultEchoDataBytes)
	if _, err := cl.Call("echo_data_bytes", params, result); err != nil {
		return []byte{}, err
	}
	return result.Value, nil
}

func testWithHTTPClient(t *testing.T, cl client.HTTPClient) {
	val := "acbd"
	got, err := echoViaHTTP(cl, val)
	require.Nil(t, err)
	assert.Equal(t, val, got)

	val2 := randBytes(t)
	got2, err := echoBytesViaHTTP(cl, val2)
	require.Nil(t, err)
	assert.Equal(t, val2, got2)

	val3 := cmn.HexBytes(randBytes(t))
	got3, err := echoDataBytesViaHTTP(cl, val3)
	require.Nil(t, err)
	assert.Equal(t, val3, got3)

	val4 := rand.Intn(10000)
	got4, err := echoIntViaHTTP(cl, val4)
	require.Nil(t, err)
	assert.Equal(t, val4, got4)
}

func echoViaWS(cl *client.WSClient, val string) (string, error) {
	params := map[string]interface{}{
		"arg": val,
	}
	err := cl.Call(context.Background(), "echo", params)
	if err != nil {
		return "", err
	}

	msg := <-cl.ResponsesCh
	if msg.Error != nil {
		return "", err

	}
	result := new(ResultEcho)
	err = json.Unmarshal(msg.Result, result)
	if err != nil {
		return "", nil
	}
	return result.Value, nil
}

func echoBytesViaWS(cl *client.WSClient, bytes []byte) ([]byte, error) {
	params := map[string]interface{}{
		"arg": bytes,
	}
	err := cl.Call(context.Background(), "echo_bytes", params)
	if err != nil {
		return []byte{}, err
	}

	msg := <-cl.ResponsesCh
	if msg.Error != nil {
		return []byte{}, msg.Error

	}
	result := new(ResultEchoBytes)
	err = json.Unmarshal(msg.Result, result)
	if err != nil {
		return []byte{}, nil
	}
	return result.Value, nil
}

func testWithWSClient(t *testing.T, cl *client.WSClient) {
	val := "acbd"
	got, err := echoViaWS(cl, val)
	require.Nil(t, err)
	assert.Equal(t, got, val)

	val2 := randBytes(t)
	got2, err := echoBytesViaWS(cl, val2)
	require.Nil(t, err)
	assert.Equal(t, got2, val2)
}

//-------------

func TestServersAndClientsBasic(t *testing.T) {
	serverAddrs := [...]string{tcpAddr, unixAddr}
	for _, addr := range serverAddrs {
		cl1 := client.NewURIClient(addr)
		fmt.Printf("=== testing server on %s using %v client", addr, cl1)
		testWithHTTPClient(t, cl1)

		cl2 := client.NewJSONRPCClient(addr)
		fmt.Printf("=== testing server on %s using %v client", addr, cl2)
		testWithHTTPClient(t, cl2)

		cl3 := client.NewWSClient(addr, websocketEndpoint)
		cl3.SetLogger(log.TestingLogger())
		err := cl3.Start()
		require.Nil(t, err)
		fmt.Printf("=== testing server on %s using %v client", addr, cl3)
		testWithWSClient(t, cl3)
		cl3.Stop()
	}
}

func TestHexStringArg(t *testing.T) {
	cl := client.NewURIClient(tcpAddr)
	// should NOT be handled as hex
	val := "0xabc"
	got, err := echoViaHTTP(cl, val)
	require.Nil(t, err)
	assert.Equal(t, got, val)
}

func TestQuotedStringArg(t *testing.T) {
	cl := client.NewURIClient(tcpAddr)
	// should NOT be unquoted
	val := "\"abc\""
	got, err := echoViaHTTP(cl, val)
	require.Nil(t, err)
	assert.Equal(t, got, val)
}

func TestWSNewWSRPCFunc(t *testing.T) {
	cl := client.NewWSClient(tcpAddr, websocketEndpoint)
	cl.SetLogger(log.TestingLogger())
	err := cl.Start()
	require.Nil(t, err)
	defer cl.Stop()

	val := "acbd"
	params := map[string]interface{}{
		"arg": val,
	}
	err = cl.Call(context.Background(), "echo_ws", params)
	require.Nil(t, err)

	msg := <-cl.ResponsesCh
	if msg.Error != nil {
		t.Fatal(err)
	}
	result := new(ResultEcho)
	err = json.Unmarshal(msg.Result, result)
	require.Nil(t, err)
	got := result.Value
	assert.Equal(t, got, val)
}

func TestWSHandlesArrayParams(t *testing.T) {
	cl := client.NewWSClient(tcpAddr, websocketEndpoint)
	cl.SetLogger(log.TestingLogger())
	err := cl.Start()
	require.Nil(t, err)
	defer cl.Stop()

	val := "acbd"
	params := []interface{}{val}
	err = cl.CallWithArrayParams(context.Background(), "echo_ws", params)
	require.Nil(t, err)

	msg := <-cl.ResponsesCh
	if msg.Error != nil {
		t.Fatalf("%+v", err)
	}
	result := new(ResultEcho)
	err = json.Unmarshal(msg.Result, result)
	require.Nil(t, err)
	got := result.Value
	assert.Equal(t, got, val)
}

// TestWSClientPingPong checks that a client & server exchange pings
// & pongs so connection stays alive.
func TestWSClientPingPong(t *testing.T) {
	cl := client.NewWSClient(tcpAddr, websocketEndpoint)
	cl.SetLogger(log.TestingLogger())
	err := cl.Start()
	require.Nil(t, err)
	defer cl.Stop()

	time.Sleep(6 * time.Second)
}

func randBytes(t *testing.T) []byte {
	n := rand.Intn(10) + 2
	buf := make([]byte, n)
	_, err := crand.Read(buf)
	require.Nil(t, err)
	return bytes.Replace(buf, []byte("="), []byte{100}, -1)
}

package srpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"srpc/codec"
	"strings"
	"sync"
	"time"
)

type Call struct {
	seq           uint64
	ServiceMethod string
	Args          interface{}
	Reply         interface{}
	Done          chan *Call
	Error         error
}

func (c *Call) done() {
	c.Done <- c
}

type Client struct {
	cc       codec.Codec
	header   codec.Header
	pendings map[uint64]*Call
	opt      *Option
	mux      sync.Mutex
	sending  sync.Mutex
	seq      uint64
	closing  bool //user has called Close
	shutdown bool //server has told us to stop
}

var ErrShutdown = errors.New("connection is shut down")

type clientRes struct {
	client *Client
	err    error
}

type newClientFunc func(conn net.Conn, opt *Option) (*Client, error)

func DialTimeout(network, addr string, opt *Option, f newClientFunc) (*Client, error) {
	if opt == nil {
		opt = DefaultOption
	}
	opt.MagicNumber = DefaultOption.MagicNumber
	if opt.CodecType == "" {
		opt.CodecType = DefaultOption.CodecType
	}
	conn, err := net.DialTimeout(network, addr, opt.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()
	ch := make(chan clientRes)
	go func() {
		if f == nil {
			f = newClient
		}
		c, e := f(conn, opt)
		ch <- clientRes{client: c, err: e}
	}()
	if opt.ConnectTimeout == 0 {
		ret := <-ch
		return ret.client, ret.err
	}
	select {
	case <-time.After(opt.ConnectTimeout):
		return nil, fmt.Errorf("rpc client: connection timeout within %s", opt.ConnectTimeout)
	case ret := <-ch:
		return ret.client, ret.err
	}
}

func newClient(conn net.Conn, opt *Option) (*Client, error) {
	err := json.NewEncoder(conn).Encode(opt)
	if err != nil {
		return nil, err
	}
	cc := codec.NewGobCodec(conn)
	client := &Client{
		cc:       cc,
		pendings: make(map[uint64]*Call),
		opt:      DefaultOption,
	}
	go client.input()
	return client, nil
}

func (c *Client) Close() error {
	c.mux.Lock()
	if c.closing {
		c.mux.Unlock()
		return ErrShutdown
	}
	c.closing = true
	c.mux.Unlock()
	return c.cc.Close()
}

func (c *Client) input() {
	var err error
	for err == nil {
		var h codec.Header
		err = c.cc.ReadHeader(&h)
		if err != nil {
			break
		}
		seq := h.Seq
		call := c.RemoveCall(seq)
		switch {
		case call == nil:
			err = c.cc.ReadBody(nil)
			if err != nil {
				err = errors.New("reading error body: " + err.Error())
			}
		case h.Error != "":
			call.Error = fmt.Errorf(h.Error)
			err = c.cc.ReadBody(nil)
			call.done()
		default:
			err = c.cc.ReadBody(call.Reply)
			if err != nil {
				call.Error = errors.New("reading body error: " + err.Error())
			}
			call.done()
		}
	}
	c.terminateCalls(err)
}

func (c *Client) terminateCalls(err error) {
	c.sending.Lock()
	defer c.sending.Unlock()
	c.mux.Lock()
	defer c.mux.Unlock()
	c.shutdown = true
	for _, call := range c.pendings {
		call.Error = err
		call.done()
	}
}

func (c *Client) send(call *Call) {
	c.sending.Lock()
	defer c.sending.Unlock()

	seq, err := c.RegisterCall(call)
	if err != nil {
		return
	}
	c.header.Seq = seq
	c.header.ServiceMethod = call.ServiceMethod
	err = c.cc.Write(&c.header, call.Args)
	if err != nil {
		call = c.RemoveCall(seq)
		if call != nil {
			call.Error = err
			call.done()
		}
	}
}

//Async
func (c *Client) Go(serviceMethod string, args, reply interface{}, done chan *Call) *Call {
	call := new(Call)
	call.ServiceMethod = serviceMethod
	call.Args = args
	call.Reply = reply
	if done == nil {
		done = make(chan *Call, 10)
	} else {
		if cap(done) == 0 {
			log.Panic("done channel is unbuffered.")
		}
	}
	call.Done = done
	c.send(call)
	return call
}

//Sync
func (c *Client) Call(ctx context.Context, serviceMethod string, args, reply interface{}) error {
	// call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	// return call.Error
	call := c.Go(serviceMethod, args, reply, make(chan *Call, 1))
	select {
	case <-ctx.Done():
		c.RemoveCall(call.seq)
		return errors.New("rpc client: call failed: " + ctx.Err().Error())
	case call := <-call.Done:
		return call.Error
	}
}

func (c *Client) RegisterCall(call *Call) (uint64, error) {
	c.mux.Lock()
	if c.shutdown || c.closing {
		c.mux.Unlock()
		call.Error = ErrShutdown
		call.done()
		return 0, ErrShutdown
	}
	seq := c.seq
	call.seq = seq
	c.seq++
	c.pendings[seq] = call
	c.mux.Unlock()
	return seq, nil
}

func (c *Client) RemoveCall(seq uint64) *Call {
	c.mux.Lock()
	defer c.mux.Unlock()
	call := c.pendings[seq]
	delete(c.pendings, seq)
	return call
}

func NewHTTPClient(conn net.Conn, opt *Option) (*Client, error) {
	_, _ = io.WriteString(conn, fmt.Sprintf("CONNECT %s HTTP/1.0\n\n", defaultRpcPath))

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err == nil && resp.Status == connected {
		return newClient(conn, opt)
	}
	if err == nil {
		err = errors.New("unexpected HTTP response: " + resp.Status)
	}
	return nil, err
}

func DialHTTP(network, addr string, opt *Option) (*Client, error) {
	return DialTimeout(network, addr, nil, NewHTTPClient)
}

// XDial calls different functions to connect to a RPC server
// according the first parameter rpcAddr.
// rpcAddr is a general format (protocol@addr) to represent a rpc server
// eg, http@10.0.0.1:7001, tcp@10.0.0.1:9999, unix@/tmp/geerpc.sock
func XDial(rpcAddr string, opt *Option) (*Client, error) {
	parts := strings.Split(rpcAddr, "@")
	if len(parts) == 2 {
		return nil, fmt.Errorf("rpc client err: wrong format '%s'", rpcAddr)
	}

	protocol, addr := parts[0], parts[1]
	switch protocol {
	case "http":
		return DialHTTP("tcp", addr, opt)
	default:
		return DialTimeout(protocol, addr, opt, nil)
	}
}

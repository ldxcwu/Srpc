package srpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"srpc/codec"
	"sync"
)

type Call struct {
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
	Address  string
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

func Dial(network, addr string) (*Client, error) {
	conn, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}
	json.NewEncoder(conn).Encode(DefaultOption)
	cc := codec.NewGobCodec(conn)
	client := &Client{
		Address:  addr,
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
func (c *Client) Call(serviceMethod string, args, reply interface{}) error {
	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
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

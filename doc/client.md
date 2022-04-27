# Client Implementation

rpc 应当提供客户端的实现以提供以下方法：
1. 拨号获得客户端对象；
   ```go
   client, err := rpc.Dial("tcp", "localhost:8080")
   ```
2. 使用客户端对象进行服务调用；
   ```go
   client.Call(serviceMethod, args, reply)
   ```
## 1. 服务调用的抽象 `Call`
表示一次的服务调用，具有以下属性：
```go
type Call struct {
	ServiceMethod string      //服务+方法名
	Args          interface{} //方法的参数
	Reply         interface{} //服务调用的返回结果
	Done          chan *Call  //用以同步服务调用
	Error         error
}
```
在服务调用的最后尝试从`Done chan`中进行读取以及
在后台获取服务执行结果之后往通道内写入可以实现服务调用的同步   
`Call`具有唯一方法，即在获取调用的结果读取之后往通道内写入数据
```go
func (c *Call) done() {
	c.Done <- c
}
```
## 2. 客户端的抽象 `Client`
```go
type Client struct {
	Address  string
	cc       codec.Codec
	header   codec.Header //服务调用时需要的信息头，可以放在Call里，但浪费空间
	pendings map[uint64]*Call
	opt      *Option
	mux      sync.Mutex
	sending  sync.Mutex   //发起调用时的锁，用以保证一次往链接内写入的信息完整
	seq      uint64 // Call对应的序号
	closing  bool   //user has called Close
	shutdown bool   //server has told us to stop
}
```
### 2.1 创建客户端
拨号获得链接，并对链接进行包括（编解码）
同时开启协程不断尝试获取服务器的反馈结果
```go
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
```
### 2.2 发送请求
```go
//异步
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

//同步
func (c *Client) Call(serviceMethod string, args, reply interface{}) error {
	call := <-c.Go(serviceMethod, args, reply, make(chan *Call, 1)).Done
	return call.Error
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
```
### 2.3 获取调用结果
因为约定的就是先发送消息头（无论请求或响应）
所以读取服务器反馈的时候，先读取消息头，
然后根据序号获得Call对象，填充结果并调用done方法
```go
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
```
## 3. 测试
```go
package main

import (
	"fmt"
	"log"
	"net"
	"srpc"
)

func startServer(addr chan string) {
	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("Listen err: ", err)
	}
	log.Println("listening on ", lis.Addr())
	addr <- lis.Addr().String()
	srpc.Accept(lis)
}

func main() {
	addr := make(chan string)
	go startServer(addr)

	client, err := srpc.Dial("tcp", <-addr)
	if err != nil {
		log.Fatal("something went wrong when dialing: ", err)
	}
	defer client.Close()
	for i := 0; i < 5; i++ {
		serviceMethod := fmt.Sprintf("ClientTest.Method%d", i)
		var reply string
		client.Call(serviceMethod, "Args", &reply)
		fmt.Println(reply)
	}
}
```
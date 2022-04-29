# 超时处理

## 1. 新增超时选项
```go
type Option struct {
	MagicNumber    int
	CodecType      string
	ConnectTimeout time.Duration //0 means no limit
	HandleTimeout  time.Duration
}
```
## 2. 建立链接超时处理
核心就是使用通道来进行超时处理 
核心代码：
```go
func DialTimeout(xxx, timeout, xxx) (ret, err) {
    ch := make(chan xxx)
    go func() {
        ret := xxx(xxx)
        ch <- ret
    }()
    if timeout == 0 {
        return <-ch
    }
    select {
        case time.After(timeout):
            return nil, errors.New("conncetion timeout")
        case ret = <-ch:
            return ret, nil
    }
}
```
详细代码：
```go
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
```
## 3. 客户端发起调用设置超时处理
一样的道理，只不过借助`context`
```go
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
```
## 4. 服务器服务请求超时处理
```go
func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup, timeout time.Duration) {
	defer wg.Done()
	ch := make(chan struct{})
	go func() {
		err := req.svc.call(req.mtype, req.argv, req.reply)
		if err != nil {
			req.h.Error = err.Error()
			server.sendResponse(cc, req.h, "invalidRequest", sending)
			ch <- struct{}{}
			return
		}
		server.sendResponse(cc, req.h, req.reply.Interface(), sending)
		ch <- struct{}{}
	}()
	if timeout == 0 {
		<-ch
		return
	}
	select {
	case <-time.After(timeout):
		req.h.Error = fmt.Sprintf("rpc server: request handle timeout within %s", timeout)
		server.sendResponse(cc, req.h, req.h.Error, sending)
		close(ch)
	case <-ch:
		return
	}
}
```
## 5. 测试单元
```go
package srpc

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestClient_dialTimeout(t *testing.T) {
	t.Parallel()
	l, _ := net.Listen("tcp", ":8080")

	f := func(conn net.Conn, opt *Option) (client *Client, err error) {
		conn.Close()
		time.Sleep(time.Second * 2)
		return nil, nil
	}
	t.Run("timeout", func(t *testing.T) {
		_, err := DialTimeout("tcp", l.Addr().String(), &Option{ConnectTimeout: time.Second}, f)
		_assert(err != nil && strings.Contains(err.Error(), "connection timeout"), "expect timeout")
	})
	t.Run("0", func(t *testing.T) {
		_, err := DialTimeout("tcp", l.Addr().String(), &Option{ConnectTimeout: 0}, f)
		_assert(err == nil, "0 means no limit")
	})
}

type Bar int

func (b Bar) Timeout(argv int, reply *int) error {
	time.Sleep(time.Second * 2)
	return nil
}

func startServer(addr chan string) {
	var b Bar
	_ = Register(&b)
	// pick a free port
	l, _ := net.Listen("tcp", ":0")
	addr <- l.Addr().String()
	Accept(l)
}

func TestClient_Call(t *testing.T) {
	t.Parallel()
	addrCh := make(chan string)
	go startServer(addrCh)
	addr := <-addrCh
	time.Sleep(time.Second)
	t.Run("client timeout", func(t *testing.T) {
		client, _ := DialTimeout("tcp", addr, nil, nil)
		ctx, _ := context.WithTimeout(context.Background(), time.Second)
		var reply int
		err := client.Call(ctx, "Bar.Timeout", 1, &reply)
		_assert(err != nil && strings.Contains(err.Error(), ctx.Err().Error()), "expect a timeout error")
	})
	t.Run("server handle timeout", func(t *testing.T) {
		client, _ := DialTimeout("tcp", addr, &Option{
			HandleTimeout: time.Second,
		}, nil)
		var reply int
		err := client.Call(context.Background(), "Bar.Timeout", 1, &reply)
		_assert(err != nil && strings.Contains(err.Error(), "handle timeout"), "expect a timeout error")
	})
}

```
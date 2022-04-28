# 服务注册
> 服务的抽象分为服务+方法
## 1. 方法
定义方法的描述，包括
- 方法的调用体`reflect.Method`类似`Invoker`
- 方法的参数以及返回值
- 方法的调用次数
```go
type methodType struct {
	method    reflect.Method
	ArgType   reflect.Type
	ReplyType reflect.Type
	numCalls  uint64
}
```
## 2. 服务
定义服务的描述，包括：
- 服务名；
- 服务类型；如果是`struct Person`,那么`Type`是`Person`，`Kind`是`Struct`
- 服务接收者，或者说调用者（`invoker`）例如`var p Person`，那么p就是`invoker`
- 服务下属方法集合
```go
type service struct {
	name    string
	typ     reflect.Type
	rcvr    reflect.Value
	methods map[string]*methodType
}
```
实现服务调用
```go
func (s *service) call(m *methodType, argv, replyv reflect.Value) error {
	atomic.AddUint64(&m.numCalls, 1)
	f := m.method.Func
	returnValues := f.Call([]reflect.Value{s.rcvr, argv, replyv})
	if errInter := returnValues[0].Interface(); errInter != nil {
		return errInter.(error)
	}
	return nil
}
```
> 服务应当提供一个注册的方法，  
> 可以通过类型使用反射创建对象，再进行注册  
> 也可以直接创建服务对象，然后进行注册（常用）
### 2.1 提供注册服务的接口
```go
//service.go
func newService(rcvr interface{}) *service {
	s := new(service)
	s.rcvr = reflect.ValueOf(rcvr)
	s.name = reflect.Indirect(s.rcvr).Type().Name()
	s.typ = reflect.TypeOf(rcvr)
	if !ast.IsExported(s.name) {
		log.Fatalf("rpc server: %s is not a valid service name", s.name)
	}
	s.registerMethods()
	return s
}
//server.go
//注册到服务器对象的map中
func (server *Server) Register(rcvr interface{}) error {
	s := newService(rcvr)
	if _, dup := server.serviceMap.LoadOrStore(s.name, s); dup {
		return errors.New("rpc: service already defined: " + s.name)
	}
	return nil
}
```
### 2.2 重构服务端收到的响应
客户端对发起的请求定义为Call   
服务端类似的，定义为request，    
重构request，对收到的请求，提取service.Method结构，寻找对应的服务+方法  
```go
func (s *Server) serveCodec(cc codec.Codec) {
	sending := new(sync.Mutex)
	wg := new(sync.WaitGroup)
	for {
        //解析出请求req
		req, err := s.readRequest(cc)
		if err != nil {
			if req == nil {
				break
			}
			req.h.Error = err.Error()
			s.sendResponse(cc, req.h, struct{}{}, sending)
			continue
		}
		wg.Add(1)
		go s.handleRequest(cc, req, sending, wg)
	}
	wg.Wait()
	_ = cc.Close()
}

func (server *Server) handleRequest(cc codec.Codec, req *request, sending *sync.Mutex, wg *sync.WaitGroup) {
	// TODO, should call registered rpc methods to get the right replyv
	// day 1, just print argv and send a hello message
	defer wg.Done()
	// req.reply = reflect.ValueOf(fmt.Sprintf("srpc resp %d", req.h.Seq))
	// server.sendResponse(cc, req.h, req.reply.Interface(), sending)
	err := req.svc.call(req.mtype, req.argv, req.reply)
	if err != nil {
		req.h.Error = err.Error()
		server.sendResponse(cc, req.h, "invalidRequest", sending)
		return
	}
	server.sendResponse(cc, req.h, req.reply.Interface(), sending)
}
```
## 3. 测试
```go
package main

import (
	"log"
	"net"
	"srpc"
	"sync"
)

type Foo int

type Args struct{ Num1, Num2 int }

func (f Foo) Sum(args Args, reply *int) error {
	*reply = args.Num1 + args.Num2
	return nil
}

func startServer(addr chan string) {
	var foo Foo
	if err := srpc.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}
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

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := &Args{Num1: i, Num2: i}
			var reply int
			if err := client.Call("Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error: ", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}

```
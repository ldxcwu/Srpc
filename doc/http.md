# ServeHTTP

`Web`开发中，经常用到`Restful`形式的各种请求和响应；   
但是`RPC`的消息格式与`HTTP`协议并不兼容，因此需要一定的转换过程   
`HTTP`协议的`CONNECT`方法提供了这种能力  
1. 浏览器向代理服务器发送`CONNECT`请求
   ```
   CONNECT ldxcwu.cc:443 HTTP/1.0
   ```
2. 代理服务器返回`HTTP 200`表示链接已经建立
   ```
   HTTP/1.0 200 Connection Established
   ```
3. 通信...    
> 其实就是一个包裹一层的过程
## 1. 服务器注册HTTP服务
由于全局ServeMux的存在，HTTP服务的注册形式及其简单
### 1.1 为服务器实现提供HTTP服务的功能
实现ServeHTTP方法即可
```go
func (s *Server) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if req.Method != "CONNECT" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusMethodNotAllowed)
		io.WriteString(w, "405 must CONNECT")
		return
	}
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		log.Print("rpc hijacking ", req.RemoteAddr, ": ", err.Error())
		return
	}
	io.WriteString(conn, "HTTP/1.0 "+connected+"\n\n")
	s.ServeConn(conn)
}
```
### 1.2 借助全局ServeMux进行注册
```go
func (s *Server) HandleHTTP() {
	http.Handle(defaultRpcPath, s)
	http.Handle(defaultDebugPath, debugHTTP{s})
	log.Println("rpc server debug path: ", defaultDebugPath)
}
```
## 2. 客户端定义HTTP拨号方法
```go
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
```
## 3. 提供浏览器debug功能（Optional）
```go
var debug = template.Must(template.New("RPC debug").Parse(debugTMP))

type debugHTTP struct {
	*Server
}

type debugService struct {
	Name   string
	Method map[string]*methodType
}

func (server debugHTTP) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var services []debugService
	server.serviceMap.Range(func(name, serv interface{}) bool {
		svc := serv.(*service)
		services = append(services, debugService{
			Name:   name.(string),
			Method: svc.methods,
		})
		return true
	})
	err := debug.Execute(w, services)
	if err != nil {
		_, _ = fmt.Fprintln(w, "rpc: error executing template: ", err.Error())
	}
}
```
## 4. 测试
### 4.1 服务端仅仅改为注册HTTP服务，并开启监听
```go
func startServer(addr chan string) {
	var foo Foo
	if err := srpc.Register(&foo); err != nil {
		log.Fatal("register error: ", err)
	}
	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatal("Listen err: ", err)
	}
	srpc.HandleHTTP()
	log.Println("listening on ", lis.Addr())
	addr <- lis.Addr().String()
	// srpc.Accept(lis)
	http.Serve(lis, nil)
}
```
### 4.2 客户端仅仅改变拨号方式
```go
func call(addr chan string) {

	// client, err := srpc.DialTimeout("tcp", <-addr, nil, nil)
	client, err := srpc.DialHTTP("tcp", <-addr, nil)
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
			if err := client.Call(context.Background(), "Foo.Sum", args, &reply); err != nil {
				log.Fatal("call Foo.Sum error: ", err)
			}
			log.Printf("%d + %d = %d", args.Num1, args.Num2, reply)
		}(i)
	}
	wg.Wait()
}
```
### 4.3 阻塞以待浏览器查看
```go
func main() {
	addr := make(chan string)
	go call(addr)
	startServer(addr)
}
```
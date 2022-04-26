# 1. 消息编解码
## 1.1 定义消息格式
将消息分为两部分：消息头+消息体  
消息头包括要调用的服务名+方法名以及序列号和错误信息
```go
type Header struct {
	ServiceMethod string //format "service.Method"
	Seq           uint64
	Error         string
}
```
消息体使用`interface{}`表示
## 1.2 消息编解码器接口
消息编解码负责往信息流里读取以及写入
```go
type Codec interface {
	io.Closer
	ReadHeader(*Header) error
	ReadBody(interface{}) error
	Write(*Header, interface{}) error
}
```
## 1.3 消息编解码器之 `Gob`
主要包括消息流载体、编解码器等
```go
type GobCodec struct {
	conn io.ReadWriteCloser
	buf  *bufio.Writer
	dec  *gob.Decoder
	enc  *gob.Encoder
}
```

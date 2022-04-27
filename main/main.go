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
	// Message Codec
	/* 	addr := make(chan string)
	   	go startServer(addr)

	   	conn, err := net.Dial("tcp", <-addr)
	   	if err != nil {
	   		log.Fatal("something wrong when dialing: ", err)
	   	}
	   	defer conn.Close()
	   	json.NewEncoder(conn).Encode(srpc.DefaultOption)
	   	cc := codec.NewGobCodec(conn)
	   	for i := 0; i < 5; i++ {
	   		h := &codec.Header{
	   			ServiceMethod: "Test.Sum",
	   			Seq:           uint64(i),
	   		}
	   		cc.Write(h, fmt.Sprintf("srpc req %d", h.Seq))
	   		cc.ReadHeader(h)
	   		var reply string
	   		cc.ReadBody(&reply)
	   		log.Println("reply: ", reply)
	   	} */

	// Implement Client
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

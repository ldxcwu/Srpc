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

package main

import (
	"fmt"
	"net"
	"time"
)

const TCPTimeLimit = 30           //TCP等待超时(seconds)
const ThreadNum = 5               //协程数
const WaitQueue = ThreadNum * 100 //等待队列长度
// 每一个协程的处理，现在只是简单打印conn
func SingleThreadWork(c1 chan net.Conn) {
	for {
		conn := <-c1
		conn.SetDeadline(time.Now().Add(time.Duration(TCPTimeLimit) * time.Second))
		for {
			fmt.Println(conn.RemoteAddr().String())
			buf := make([]byte, 1024)
			_, err := conn.Read(buf) // 从conn中读取客户端发送的数据内容
			if err != nil {
				fmt.Println("exit?")
				break
			}
			fmt.Println(string(buf))
		}
		conn.Close()
	}
}

func main() {
	listen, err := net.Listen("tcp", ":8888") // 创建用于监听的 socket
	if err != nil {
		fmt.Println("listen err=", err)
		return
	}
	fmt.Println("监听套接字，创建成功, 服务器开始监听。。。")
	defer listen.Close() // 服务器结束前关闭 listener

	connchan := make(chan net.Conn, WaitQueue)
	for i := 0; i < ThreadNum; i++ {
		go SingleThreadWork(connchan)
	}
	// 循环等待客户端链接
	for {
		fmt.Println("阻塞等待客户端链接...")
		conn, err := listen.Accept() // 创建用户数据通信的socket

		if err != nil {
			panic("Accept() err=  " + err.Error())
		}
		connchan <- conn
	}
}

/*http://127.0.0.1:8888/api/camera/get_ptz?camera_id=1324566666789876543*/

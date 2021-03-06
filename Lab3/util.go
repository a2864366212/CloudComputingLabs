package main

import (
	"bufio"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

type command struct {
	cmdType int
	key     []string
	value   string
	taskid  string
}

//基本配置信息
var mode = "coordinator"
var configPath = "./src/coordinator.conf"
var coordinatorIPPort = "175.10.105.61:8001"
var participantIPPortArr []string

//存储数据的内存空间
var database map[string]string

//响应coordinator请求的工作协程
var debugClientHandle = true
var connParticipant [3]net.Conn

//心跳检测
var heartbeatsCnt = [3]int{0, 0, 0}

const HeartBeatsResps = "*0\r\n"

var finCnt2PC uint64

//指令的解析与封装
/*
	set key val
	get key
	del key_{0} key_{1}... key_{n}
*/
const (
	set          = 0
	get          = 1
	del          = 2
	heartBeats   = 3
	prepare      = 4
	commit       = 5
	rollback     = 6
	syndata      = 7 //协同者->参与者，让参与者与最新参与者同步数据
	synget       = 8 //参与者与参与者之间同步数据
	synTargetGet = 9 //协调者广播所有活的参与者结点,获取最新的参与者IP信息
)
const SUCCESS = "+OK\r\n"
const FAIL = "-ERROR\r\n"

func readConfig() {
	/*读取配置文件
	设置mode的值，运行在协同者或是参与者模式
	设置coordinatorIPPort与participantIPPort[]的值
	设置
	*/
	configPathInput := flag.String("config_path", "./src/coordinator.conf", "What is your configPath?")
	flag.Parse() //解析输入的参数
	configPath = *configPathInput

	f, err := os.Open(configPath)
	if err != nil {
		print(err.Error())
		return
	}
	defer f.Close()
	input := bufio.NewScanner(f)
	for input.Scan() {
		st := input.Text()
		if len(st) >= 4 && st[:4] == "mode" {
			mode = st[5:]
		} else if len(st) >= 16 && st[:16] == "coordinator_info" {
			coordinatorIPPort = st[17:]
		} else if len(st) >= 16 && st[:16] == "participant_info" {
			participantIPPortArr = append(participantIPPortArr, st[17:])
		}
	}
	fmt.Println(mode)
	fmt.Println(configPath)
	fmt.Println(coordinatorIPPort)
	for _, participantIPPort := range participantIPPortArr {
		fmt.Println(participantIPPort)
	}
}

func parseCmd(RESPArraysStr string) command {
	//解析RESP Arrays格式指令
	//*4 $3 SET $7 CS06142 $5 Cloud $9 Computing
	//小字符串直接split

	RESPArraysTmp := strings.Split(RESPArraysStr, "\r\n")
	RESPArraysTmp = RESPArraysTmp[:len(RESPArraysTmp)-1]
	// if debugClientHandle {
	// 	print("parseCmd() debug info")
	// 	for i, ele := range RESPArraysTmp {
	// 		fmt.Println(i, ":", ele)
	// 	}
	// }
	var RESPArrays []string
	arraySize, _ := strconv.Atoi(RESPArraysTmp[0][1:])
	if arraySize == 0 {
		//心跳包：空包
		heartBeatsPacket := command{cmdType: heartBeats}
		return heartBeatsPacket
	}
	//arraySize>=1
	var i = 2
	for {
		if i > arraySize*2 {
			break
		}
		RESPArrays = append(RESPArrays, RESPArraysTmp[i])
		i = i + 2
	}
	cmd := command{}
	cmdTypeStr := RESPArrays[0]
	if cmdTypeStr == getCmdStr(set) {
		setCmd := command{cmdType: set}
		setCmd.key = append(setCmd.key, RESPArrays[1])
		setCmd.value = RESPArrays[2]
		cmd = setCmd
	}
	if cmdTypeStr == getCmdStr(get) {
		getCmd := command{cmdType: get}
		getCmd.key = append(getCmd.key, RESPArrays[1])
		cmd = getCmd
	}
	if cmdTypeStr == getCmdStr(del) {
		delCmd := command{cmdType: del}
		for i = 1; i < arraySize; i++ {
			delCmd.key = append(delCmd.key, RESPArrays[i])
		}
		cmd = delCmd
	}
	if cmdTypeStr == getCmdStr(commit) {
		comCmd := command{cmdType: commit}
		cmd = comCmd
	}
	if cmdTypeStr == getCmdStr(rollback) {
		rollCmd := command{cmdType: rollback}
		cmd = rollCmd
	}
	if cmdTypeStr == getCmdStr(syndata) {
		/*RESP Arrays 格式 ：req: syndata IP:Port
		req ack:  syndata ACK
		fin: syndata FIN
		fin ack: syndata FIN_ACK
		*/
		syndataCmd := command{cmdType: syndata}
		syndataCmd.value = RESPArrays[1] // IP:Port-->RESPArrays[1]
		cmd = syndataCmd
	}
	if cmdTypeStr == getCmdStr(synget) {
		/*RESP Arrays 格式 ：
		1.待同步参与者向目标参与者请求同步，req: synget
				2.loop:
					2.1 目标参与者回复待同步参与者某个key：使用之前的标准set格式  set key val
					2.2 待同步参与者向目标参与者回复ACK：+OK
		*/
		syngetCmd := command{cmdType: synget}
		cmd = syngetCmd
	}
	if cmdTypeStr == getCmdStr(synTargetGet) {
		/*
			RESP Arrays 格式 ：req: synTargetGet
							  ack: synTargetGet cntFlag(cntFlag是参与者最近一次执行命令后的计数，每次完成一次两阶段提交计数值+1)
		*/
		synTargetGetCmd := command{cmdType: synTargetGet}
		if arraySize >= 2 {
			synTargetGetCmd.value = RESPArrays[1] //cntFlag-->RESPArrays[1]
		}
		cmd = synTargetGetCmd
	}
	//大字符串读取？
	/*var RESPArray []string //字符串序列 e.g. []arr={set,key,val}
	var err error
	var i =0
	for{
		if RESPArraysStr[i]=='*'||RESPArraysStr[i]=='$'{
			var eleSize int
			sizeStr:=""
			sizeBeginIdx:=i+1
			chType:=RESPArraysStr[i]
			for{
				if RESPArraysStr[i:i+2]=="\r\n" {
					sizeStr=RESPArraysStr[sizeBeginIdx:i]
					eleSize,err =strconv.Atoi(sizeStr)
					break
				}
				i=i+1
			}
			if chType=='$'{
				i=i+2
			}
		}

		i=i+1
		if i>= len(RESPArraysStr){
			break
		}
	}*/
	return cmd
}
func getCmdStr(cmdType int) string {
	if cmdType == set {
		return "SET"
	}
	if cmdType == get {
		return "GET"
	}
	if cmdType == del {
		return "DEL"
	}
	if cmdType == commit {
		return "commit"
	}
	if cmdType == rollback {
		return "rollback"
	}
	if cmdType == heartBeats {
		return "heartBeats"
	}
	if cmdType == syndata {
		return "syndata"
	}
	if cmdType == synget {
		return "synget"
	}
	if cmdType == synTargetGet {
		return "synTargetGet"
	}
	return ""
}
func cmd2RESPArr(cmd command) string {
	//封装指令为RESP Arrays
	/*
		set        = 0    set key value
		get        = 1    get key
		del        = 2    del key1 key2 keyi
	*/

	valueStrLen := len(cmd.value)
	var valueSplit []string
	if valueStrLen > 0 {
		valueSplitTmp := strings.Split(cmd.value, " ")
		for _, val := range valueSplitTmp {
			valueSplit = append(valueSplit, val)
		}

	}
	RESPArraysSize := 1 + len(cmd.key) + len(valueSplit)
	RESPArraysStr := "*" + strconv.Itoa(RESPArraysSize) + "\r\n"
	//append cmdType
	RESPArraysStr = RESPArraysStr + "$" + strconv.Itoa(len(getCmdStr(cmd.cmdType))) + "\r\n" + getCmdStr(cmd.cmdType) + "\r\n"
	//append key[]
	for _, key := range cmd.key {
		RESPArraysStr = RESPArraysStr + "$" + strconv.Itoa(len(key)) + "\r\n" + key + "\r\n"
	}
	for _, val := range valueSplit {
		RESPArraysStr = RESPArraysStr + "$" + strconv.Itoa(len(val)) + "\r\n" + val + "\r\n"
	}
	return RESPArraysStr
}
func str2RESPArr(str string) string {
	stringArr := strings.Split(str, " ")
	RESPArraysStr := "*" + strconv.Itoa(len(stringArr)) + "\r\n"
	for _, ele := range stringArr {
		RESPArraysStr = RESPArraysStr + "$" + strconv.Itoa(len(ele)) + "\r\n" + ele + "\r\n"
	}
	return RESPArraysStr
}

package main

import (
	"fmt"
	"bufio"
	"os"
	"./eyetribe"
)

func main(){
	eye, err := eyetribe.CreateServerConnection("localhost:6555")
	if err != nil {
		fmt.Printf("can not connect to server: %q\n", err)
		return
	}
	err = eye.SetLogFile("log.json")
	if err != nil {
		fmt.Printf("log file open error:%q\n", err)
		return
	}
	err = eye.LoadEyeTrackCheckConfig("config.json")
	if err != nil {
		fmt.Printf("config file load error:%q\n", err)
		return
	}
	fmt.Println("start!")
	eye.StartPullFrameTask(30) // 30秒分溜め込ませます
	eye.StartHttpService(8888)

	fmt.Println("\"q\" を入力して Enter で終了します。その他の Enger入力 で config.json を読み直します。")
	bio := bufio.NewReader(os.Stdin)
	for {
		line_bin, _, _ := bio.ReadLine()
		line_str := string(line_bin)
		if len(line_str) > 0 && line_str[0] == "q"[0]{
			break
		}
		fmt.Println("設定ファイルを読み直します。")
		err = eye.LoadEyeTrackCheckConfig("config.json")
		if err != nil {
			fmt.Printf("config file load error:%q\n", err)
			return
		}
	}

	fmt.Println("exit now.")
	eye.Close()
}



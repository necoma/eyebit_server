package eyetribe

import (
	"net"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
	"container/list"
	"image"
	"image/png"
	"image/draw"
	"net/http"
	"strconv"
)

// 接続状態等を保存するための構造体
type JsonReaderWriter struct {
	Decoder *json.Decoder
	Encoder *json.Encoder
	Connection *net.TCPConn
}

// こちらからのリクエスト型(汎用)
type RequestMessage struct {
	Category string    `json:"category"`
	Request string     `json:"request"`
	Values interface{} `json:"values"`
}

// サーバからの返事型(汎用)
type ResponseMessage struct {
	Category   string      `json:"category"`
	Request    string      `json:"request"`
	StatusCode int         `json:"statuscode"`
	Values     map[string]interface{} `json:"values"`
}

// こちらからのリクエスト型(heartbeat専用)
type RequestHeartBeatMessage struct {
	category string
}

// こちらからのリクエスト型(push mode 変更専用)
type RequestPushModeMessageValue struct {
	Push bool   `json:"push"`
	Version int `json:"version"`
}

// こちらからのリクエスト型(push mode 変更専用)
type RequestPushModeMessage struct {
	Category string `json:"category"`
	Request string  `json:"request"`
	Values *RequestPushModeMessageValue `json:"values"`
}

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type EyeData struct {
	Raw *Point `json:"raw"`
	Avg *Point `json:"avg"`
	Psize float64 `json:"psize"`
	Pcenter *Point `json:"pcenter"`
}

// EyeTribe の一つのフレーム(アイトラッキングデータ)
type Frame struct {
	Timestamp string `json:"timestamp"`
	Time float64 `json:"time"`
	Fix bool `json:"fix"`
	State int64 `json:"state"`
	Raw *Point `json:"raw"`
	Avg *Point `json:"avg"`
	LeftEye *EyeData `json:"lefteye"`
	RightEye *EyeData `json:"righteye"`
	GoTime time.Time
}

// 一つだけのフレームのメッセージ
type OneFrameMessage struct {
	Category string `json:"category"`
	Request string `json:"request"`
	StatusCode int `json:"statuscode"`
	Values map[string]*Frame `json:"values"`
}

// 指定の場所を確認していたかどうかの指定の場所
type EyeTrackCheckPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Width float64 `json:"width"`
	Height float64 `json:"height"`
	Name string `json:"name"`
}

// 指定の場所を確認していたかどうかを判定するための設定
type EyeTrackCheckConfig struct {
	Fixation *map[string]int `json:"fixation"` // そこを見ていたと判定される時に使う情報
	TargetList []*EyeTrackCheckPoint `json:"targets"` // 対象の情報
}

// 指定の場所を確認していたかどうかの判定結果
type EyeTrackCheckResult map[string]bool

// EyeTribe を使うための class
type EyeTribeConnection struct {
	Connection *JsonReaderWriter
	ScreenWidth int64
	ScreenHeight int64
	HeartbeatTimeoutMillisecond int64
	FrameList *list.List
	QuitHeartbeatTask chan bool
	QuitPullTask chan bool
	HeatMapDrawImage *image.Image
	CheckConfig EyeTrackCheckConfig
	LogFile *os.File
}

// 見ていた(Fixation チェックに成功した)とされる座標とその時間を記録したデータ
type FixateData struct {
	X float64
	Y float64
	GoTime time.Time
}

// リクエスト型をJsonにエンコードしたらどうなるかを Stdout に吐き出します
func (m *RequestMessage) DumpJsonToStdout(){
	encoder := json.NewEncoder(os.Stdout)
	encoder.Encode(m)
}

// heartbeat を送るための gorutine を作ります。
// intervalの時間間隔で heartbeat を送ろうとします。
// 終了させるには StopHeartbeatTask() を呼び出します。
func (c *EyeTribeConnection) StartHeartbeatTask(interval time.Duration) {
	c.QuitHeartbeatTask = make(chan bool)
	tick := time.Tick(interval)
	go func(){
		quitFlug := false
		for(quitFlug != true) {
			select {
			case <- c.QuitHeartbeatTask:
				quitFlug = true
				break
			case <- tick:
				data := RequestMessage{
					Category: "heartbeat",
				}
				//fmt.Println("send heartbeat message.")
				if err := c.Connection.PushOneJson(&data); err != nil {
					fmt.Printf("heartbeat: PushOneJson() error. quit: %q\n", err)
					quitFlug = true
					break
				}
			}
		}
	}()
}

// heartbeat タスクを終了します
func (c *EyeTribeConnection) StopHeartbeatTask() {
	if c.QuitHeartbeatTask == nil {
		return
	}
	c.QuitHeartbeatTask <- true
	c.QuitHeartbeatTask = nil
}

// サーバに接続します
// host_and_port は "hostname:port" と書きます
func CreateServerConnection(host_and_port string) (*EyeTribeConnection, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", host_and_port)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return nil, err
	}
	ret := &EyeTribeConnection {
		Connection: &JsonReaderWriter{
			Decoder: json.NewDecoder(conn),
			Encoder: json.NewEncoder(conn),
			Connection: conn,
		},
		FrameList: list.New(),
	}
	calibrated, interval, err := ret.GetServerStatus()
	if err != nil {
		return nil, err
	}
	if calibrated != true {
		return nil, errors.New("Server is not calibrated")
	}
	ret.StartHeartbeatTask(time.Duration(int64(interval) / 2))

	return ret, nil
}

// 接続を切ります
func (rw *JsonReaderWriter) Close() error {
	if rw == nil {
		return errors.New("nil input")
	}
	if rw.Connection != nil {
		return rw.Connection.Close()
	}
	return nil
}

// サーバとの接続を切り、不要なタスクを終了します。
func (c *EyeTribeConnection) Close() error {
	if c == nil {
		return errors.New("nil input")
	}
	c.StopHeartbeatTask()
	c.StopPullFrameTask()

	return c.Connection.Close()
}

// 一つのJSONメッセージを送信します
func (rw *JsonReaderWriter) PushOneJson(input *RequestMessage) error{
	if rw == nil {
		return errors.New("nil input")
	}
	err := rw.Encoder.Encode(input)
	if err != nil {
		return err
	}
	return nil
}

// 一つのJSONメッセージを受信します
func (rw *JsonReaderWriter) PullOneJson() (*ResponseMessage, error){
	if rw == nil {
		return nil, errors.New("nil input")
	}
	var ret ResponseMessage
	err := rw.Decoder.Decode(&ret)
	if err != nil {
		return nil, err
	}
	return &ret, nil
}

// 一つのJSONメッセージを送信して、一つのJSONメッセージを受信します
func (rw *JsonReaderWriter) RequestOne(input *RequestMessage) (*ResponseMessage, error){
	err := rw.PushOneJson(input)
	if err != nil {
		return nil, err
	}
	return rw.PullOneJson()
}

func ConvInterfaceToInt64(v map[string]interface{}, name string) (int64, error) {
	var ok bool
	val, ok := v[name]
	if ok == false {
		return 0, errors.New(fmt.Sprintf("value has no \"%s\" field", name))
	}
	return int64(val.(float64)), nil
}

// サーバの状態を確認します。
// bool → キャリブレートされているか
// int64 → heartbeat interval
func (c *EyeTribeConnection) GetServerStatus() (bool, time.Duration, error) {
	data := RequestMessage{
		Category: "tracker",
		Request: "get",
		Values: []string{"push", "iscalibrated",
			"heartbeatinterval", "screenresw", "screenresh",
			"framerate"},
	}

	response, err := c.Connection.RequestOne(&data)
	if err != nil {
		return false, 0, err
	}
	fmt.Printf("response: %q\n", response)
	if response.StatusCode != 200 {
		return false, 0, errors.New("server response code is not 200")
	}

	var ok bool
	iscalibrated, ok := response.Values["iscalibrated"]
	if ok == false {
		return false, 0, errors.New("server response has no iscalibrated field")
	}

	interval, err := ConvInterfaceToInt64(response.Values, "heartbeatinterval")
	if err != nil {
		return false, 0, err
	}
	if interval <= 0 {
		return false, 0, errors.New(fmt.Sprintf("server return heartbeatinterval is invalid: %p", interval))
	}

	c.ScreenWidth, err = ConvInterfaceToInt64(response.Values, "screenresw")
	if err != nil {
		return false, 0, err
	}
	c.ScreenHeight, err = ConvInterfaceToInt64(response.Values, "screenresh")
	if err != nil {
		return false, 0, err
	}
	c.HeartbeatTimeoutMillisecond, err = ConvInterfaceToInt64(response.Values, "framerate")
	if err != nil {
		return false, 0, err
	}
	
	return iscalibrated.(bool), time.Duration(interval) * time.Millisecond, nil
}

// []byte を log に書き出します。
func (c *EyeTribeConnection) PutLog(data []byte) error {
	if c == nil {
		return errors.New("this is nil")
	}
	if c.LogFile == nil {
		return errors.New("log file not opend")
	}
	_, err := c.LogFile.Write(data)
	if err != nil {
		return err
	}
	_, err = c.LogFile.Write([]byte("\n"))
	return err
}

func (c *EyeTribeConnection) PutLogJson(msg OneFrameMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.PutLog(data)
}

func (c *EyeTribeConnection) PutLogString(str string) error {
	return c.PutLog([]byte(str))
}

// pullリクエストで一つフレームを取り出します。
func (c *EyeTribeConnection) PullOneFrame() (*Frame, error) {
	var response OneFrameMessage
	err := c.Connection.Decoder.Decode(&response)
	if err != nil {
		fmt.Printf("decode error: %q\n", err)
		return nil, err
	}
	//fmt.Printf("responce: %q\n", response)
	
	if response.StatusCode != 200 {
		fmt.Printf("responce.statuscode != 200: \n")
		return nil, nil
		//return nil, errors.New(fmt.Sprintf("server return status code is not 200 (%d)", response.statuscode))
	}
	if response.Category == "heartbeat" {
		// heartbeat は無視します。
		return nil, nil
	}
	var ok bool
	frame, ok := response.Values["frame"]
	if ok == false {
		fmt.Printf("response has no frame field")
		return nil, nil
	}
	frame.GoTime = time.Now()
	c.PutLogJson(response)
	return frame, nil
}

// フレームを一つキャッシュに貯めます。
// numFrames 以上のものは溢れるような処理をします。
func (c *EyeTribeConnection) AddOneFrame(frame *Frame, numFrames int) error {
	if c == nil {
		// nil は許容します
		return nil
	}
	if c.FrameList.Len() >= numFrames {
		c.FrameList.Remove(c.FrameList.Front())
	}
	c.FrameList.PushBack(frame)
	//fmt.Printf("frame: %q\n", frame)
	return nil
}

// push mode でデータの取得を開始します。
// だいたい second[秒] 分の frame を溜め込むようにします。
func (c *EyeTribeConnection) StartPullFrameTask(second int64) {
	// サーバを push mode にします。
	pushModeMessage := &RequestPushModeMessage{
		Category: "tracker",
		Request: "set",
		Values: &RequestPushModeMessageValue{Push: true, Version: 1},
	}
	err := c.Connection.Encoder.Encode(pushModeMessage)
	if err != nil {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.Encode(&pushModeMessage)

	numFrames := int(second * 1000 / c.HeartbeatTimeoutMillisecond)
	fmt.Printf("numFrames: %d\n", numFrames)

	c.QuitPullTask = make(chan bool)
	go func(){
		quitFlug := false
		for (quitFlug != true) {
			select {
			case <- c.QuitPullTask:
				quitFlug = true
				break
			default:
				frame, err := c.PullOneFrame()
				if err != nil {
					fmt.Printf("pull one frame return error: %q\n", err)
					quitFlug = true
					break
				}
				err = c.AddOneFrame(frame, numFrames)
				if err != nil {
					fmt.Printf("add one frame return error: %q\n", err)
					quitFlug = true
					break
				}
			}
		}
	}()
}

func (c *EyeTribeConnection) StopPullFrameTask() error {
	if c == nil {
		return errors.New("nil input")
	}
	if c.QuitPullTask == nil {
		return errors.New("push task is not started")
	}
	c.QuitPullTask <- true
	return nil
}

func (c *EyeTribeConnection) StopHttpService() {
	
}


func (c *EyeTribeConnection) GetFrameList() *list.List {
	return c.FrameList
}

func (c *EyeTribeConnection) LoadHeatMapDrawImage() (*image.Image, error) {
	if c.HeatMapDrawImage != nil {
		return c.HeatMapDrawImage, nil
	}
	imgFile, err := os.Open("heatmap_brush.png")
	if err != nil {
		return nil, err
	}
	defer imgFile.Close()
	img, err := png.Decode(imgFile)
	if err != nil {
		return nil, err
	}
	c.HeatMapDrawImage = &img
	return &img, nil
}

func (c *EyeTribeConnection) CreateHeatMapImage() (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, int(c.ScreenWidth), int(c.ScreenHeight)) )
	draw.Draw(img, img.Bounds(), image.Transparent, image.ZP, draw.Src)

	drawImage, err := c.LoadHeatMapDrawImage()
	if err != nil {
		return nil, err
	}
	drawWidth := 100.0
	drawHeight := 100.0
	for f := c.FrameList.Front(); f != nil; f = f.Next() {
		frame := f.Value.(*Frame)
		if frame == nil || frame.Avg == nil {
			continue
		}
		if frame.Avg.X <= 0.0 && frame.Avg.Y <= 0.0 {
			// 外れ値っぽいので無視します。
			continue
		}
		x := frame.Avg.X
		y := frame.Avg.Y
		x_start := x - drawWidth / 2.0
		y_start := y - drawHeight / 2.0
		x_end := x + drawWidth / 2.0
		y_end := y + drawHeight / 2.0
		draw.Draw(img, image.Rect(int(x_start), int(y_start), int(x_end), int(y_end)),
			*drawImage, image.ZP, draw.Over)
	}
	return img, nil	
}

func (c *EyeTribeConnection) ServeHeatMapPng(w http.ResponseWriter, r *http.Request){
	img, err := c.CreateHeatMapImage()
	if err != nil {
		w.Write([]byte("internal server error: create PNG failed."))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	png.Encode(w, img)
}

// 現在持っている情報から 注視 していた座標のリストを返します。
func (c *EyeTribeConnection) GetFixationDataList() []FixateData {
	result := []FixateData{}
	fixation := c.CheckConfig.Fixation
	var ok bool
	max_distance, ok := (*fixation)["max distance"]
	if !ok {
		max_distance = 50
	}
	min_msec, ok := (*fixation)["min msec"]
	if !ok {
		min_msec = 100
	}

	PrevX := -1000000.0
	PrevY := -1000000.0
	PrevTime := time.Now()
	CurrentTime := time.Now()
	front := c.FrameList.Front()
	if front != nil {
		front_frame := front.Value.(*Frame)
		if front_frame != nil {
			PrevTime = front.Value.(*Frame).GoTime
			CurrentTime = PrevTime
			if front_frame.Avg != nil {
				PrevX = front_frame.Avg.X
				PrevY = front_frame.Avg.Y
			}
		}
	}
	check_time := PrevTime.Add(time.Millisecond * time.Duration(min_msec))
	AverageX := PrevX
	AverageY := PrevY
	fixate_count := 0
	fixate_count_sum := 0
	distance2 := float64(max_distance * max_distance)
	for f := c.FrameList.Front(); f != nil; f = f.Next() {
		frame := f.Value.(*Frame)
		if frame == nil || frame.Avg == nil {
			continue
		}
		if frame.Avg.X <= 0.0 && frame.Avg.Y <= 0.0 {
			// 外れ値っぽいので無視します。
			continue
		}
		X := frame.Avg.X
		Y := frame.Avg.Y
		t := frame.GoTime
		// 今見ている所が前の所より max_distance 以上離れていれば
		// 違う場所を見始めたとする
		xx := PrevX - X
		yy := PrevY - Y
		if distance2 < (xx * xx + yy * yy) {
			// 違う場所を見始めた か、

			// 違う場所を見始めるまでに指定された時間が経っていて、
			// 何回も計測されているなら、見続けた事にする。
			//fmt.Printf("%d count. total %q (%f,%f)\r\n", fixate_count, check_time.Sub(t), AverageX, AverageY)
			if check_time.Sub(t) < 0 && fixate_count > 1 {
				//fmt.Printf("add %d count. total %q\r\n", fixate_count, check_time.Sub(t))
				result = append(result, FixateData{AverageX, AverageY, PrevTime})
			}

			fixate_count = 0
			PrevTime = t
			check_time = PrevTime.Add(time.Millisecond * time.Duration(min_msec))
			PrevX = X
			PrevY = Y
			AverageX = PrevX
			AverageY = PrevY
			continue
		}
		// ここまで来たのであれば、前の所からそれほど離れていない所を見ていたことになる
		// ということで、見続けたカウントを増やします。
		fixate_count += 1
		fixate_count_sum += 1
		CurrentTime = t

		alpha := 1.0 / (1.0 + float64(fixate_count))
		a := 1.0 - alpha
		b := alpha
		AverageX = a * AverageX + b * PrevX
		AverageY = a * AverageY + b * PrevY
	}
	// 最後に残ったものも追加する必要があれば追加します。
	if fixate_count > 0 && check_time.Sub(CurrentTime) < 0 {
		//fmt.Printf("%d count. total %q (%f,%f)\r\n", fixate_count, check_time.Sub(CurrentTime), AverageX, AverageY)
		result = append(result, FixateData{AverageX, AverageY, PrevTime})
	}
	fmt.Printf("注視回数, 微小移動回数, 全体の回数 -> %d, %d, %d\r\n", len(result), fixate_count_sum, c.FrameList.Len())
	return result
}

// 単に一瞬でも見ていればOKとする場合
func (c *EyeTribeConnection) ServeEyeTrackCheck(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type", "application/json")
	delta_millisecond := 10*1000 // default は 10秒前までのデータを確認します。
	// delta_millisecond が指定されていたら、その秒数までのデータで確認しようとします。
	delta_millisecond_str := r.FormValue("delta_millisecond")
	millisecond, err := strconv.Atoi(delta_millisecond_str)
	if err == nil {
		delta_millisecond = millisecond
	}
	check_time := time.Now().Add(-time.Duration(delta_millisecond) * time.Millisecond)
	
	result := make(EyeTrackCheckResult)
	for f := c.FrameList.Front(); f != nil; f = f.Next() {
		frame := f.Value.(*Frame)
		if frame == nil || frame.Avg == nil {
			continue
		}
		if check_time.Sub(frame.GoTime) > 0 {
			continue
		}
		if frame.Avg.X <= 0.0 && frame.Avg.Y <= 0.0 {
			// 外れ値っぽいので無視します。
			continue
		}
		x := frame.Avg.X
		y := frame.Avg.Y

		for i := range c.CheckConfig.TargetList {
			v := c.CheckConfig.TargetList[i]
			if v == nil {
				continue
			}
			if x >= v.X && x <= (v.X + v.Width) && y >= v.Y && y <= (v.Y + v.Height) {
				result[v.Name] = true
			}else{
				result[v.Name] = false
			}
		}
	}
	fmt.Printf("result: %q\n", result)
	encoder := json.NewEncoder(w)
	encoder.Encode(&result)
}

// 注視していればOKとする場合
func (c *EyeTribeConnection) ServeEyeTrackCheckFixation(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type", "application/json")
	delta_millisecond := 10*1000 // default は 10秒前までのデータを確認します。
	// delta_millisecond が指定されていたら、その秒数までのデータで確認しようとします。
	delta_millisecond_str := r.FormValue("delta_millisecond")
	millisecond, err := strconv.Atoi(delta_millisecond_str)
	if err == nil {
		delta_millisecond = millisecond
	}
	check_time := time.Now().Add(-time.Duration(delta_millisecond) * time.Millisecond)
	
	result := make(EyeTrackCheckResult)
	data_list := c.GetFixationDataList()
	for i := range data_list {
		data := data_list[i]
		if check_time.Sub(data.GoTime) > 0 {
			continue
		}
		x := data.X
		y := data.Y
		for i := range c.CheckConfig.TargetList {
			v := c.CheckConfig.TargetList[i]
			if v == nil {
				continue
			}
			if x >= v.X && x <= (v.X + v.Width) && y >= v.Y && y <= (v.Y + v.Height) {
				result[v.Name] = true
			}else{
				result[v.Name] = false
			}
		}
	}
	fmt.Printf("result: %q\n", result)
	encoder := json.NewEncoder(w)
	encoder.Encode(&result)
}

func (c *EyeTribeConnection) StartHttpService(port int) error {
	http.HandleFunc("/current_heatmap.png", func(w http.ResponseWriter, r *http.Request){
		c.ServeHeatMapPng(w, r)
	})
	http.HandleFunc("/check.json", func(w http.ResponseWriter, r *http.Request){
		c.ServeEyeTrackCheck(w, r)
	})
	http.HandleFunc("/check_fixation.json", func(w http.ResponseWriter, r *http.Request){
		c.ServeEyeTrackCheckFixation(w, r)
	})
	fileServer := http.FileServer(http.Dir("./static/"))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		msg := fmt.Sprintf("{\"request path\": \"%s\", \"unix time\": %d}", r.RequestURI, time.Now().Unix())
		c.PutLogString(msg)
		fileServer.ServeHTTP(w, r)
	})
	go func(){
		//http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
		http.ListenAndServeTLS(fmt.Sprintf(":%d", port), "ssl_key/server.crt", "ssl_key/server.key", nil)
	}()
	fmt.Println("httpd done")
	return nil
}

func (c *EyeTribeConnection) LoadEyeTrackCheckConfig(fileName string) error {
	configFile, err := os.Open(fileName)
	if err != nil {
		return err
	}
	defer configFile.Close()
	decoder := json.NewDecoder(configFile)
	return decoder.Decode(&c.CheckConfig)
}

func (c *EyeTribeConnection) SetLogFile(fileName string) error {
	file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0660)
	if err == nil {
		c.LogFile = file
	}
	return err
}

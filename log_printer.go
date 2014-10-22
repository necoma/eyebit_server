package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"image"
	"image/png"
	"image/draw"
	"bufio"
	"io"
	"strings"
	"flag"
	"io/ioutil"
)

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
	GoTime time.Time `json:"GoTime"`
}

// 一つだけのフレームのメッセージ
type OneFrameMessage struct {
	Category string `json:"category"`
	Request string `json:"request"`
	StatusCode int `json:"statuscode"`
	Values map[string]*Frame `json:"values"`
}

type RequestPath struct {
	RequestPath string `json:"request path"`
	UnixTime int64 `json:"unix time"`
}

// 一つのWebPage用のlog
type OneWebPageTrackLog struct {
	FrameArray []*Frame
	Url string
	UnixTime int64 // log の取られたUnix時間
	ImageFileNameList []string // 生成された画像ファイルの名前リスト
}

func LoadPngImage(fileName string) (*image.Image, error) {
	imgFile, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer imgFile.Close()
	img, err := png.Decode(imgFile)
	if err != nil {
		return nil, err
	}
	return &img, nil
}

func CreateHeatMapImage(heatMapImage *image.Image, log OneWebPageTrackLog, ScreenWidth int, ScreenHeight int, maxSecond int64, startUnixTime int64) (*image.RGBA, error) {
	img := image.NewRGBA(image.Rect(0, 0, int(ScreenWidth), int(ScreenHeight)) )
	draw.Draw(img, img.Bounds(), image.Transparent, image.ZP, draw.Src)

	drawImage := heatMapImage
	drawWidth := 100.0
	drawHeight := 100.0
	maxTime := time.Unix(startUnixTime + maxSecond, 0)
	for i := 0; i < len(log.FrameArray) ; i++ {
		frame := log.FrameArray[i]
		if frame == nil || frame.Avg == nil {
			continue
		}
		if frame.Avg.X <= 0.0 && frame.Avg.Y <= 0.0 {
			// 外れ値っぽいので無視します。
			continue
		}
		if maxSecond > 0 && maxTime.Sub(frame.GoTime) < 0 {
			// maxSecond が 0より大きい指定であれば、その時間までしか見ないで良いです。
			break
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

type MainFlags struct {
	LogFileName string
	DirectoryName string
	ImageConfigFileName string
}

type ImageConfigUnit struct {
	ImageMap map[string]string `json:"image_map"`
}

// エラーは返さず空のデータを返します
func LoadImageConfig(fileName string) map[string]string {
	var result map[string]string
	
	buf, err := ioutil.ReadFile(fileName)
	if err != nil {
		return result
	}

	err = json.Unmarshal(buf, &result)
	if err != nil {
		fmt.Printf("file %s json decode error: %q\n", err, fileName)
		return map[string]string{}
	}

	return result
}

func SaveHeatMapImage(fileName string, log OneWebPageTrackLog, width int, height int, maxSecond int64, startUnixTime int64, heatMapImage *image.Image, addImage *image.Image) error {
	fmt.Printf("  creating image %s (%s)...\n", fileName, log.Url)
	img, err := CreateHeatMapImage(heatMapImage, log, width, height, maxSecond, startUnixTime)
	if err != nil {
		fmt.Printf("heatmap image create error: %q\n", err)
		return err
	}
	imgFile, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Printf("heatmap image file create error: %q\n", err)
		return err
	}
	if addImage != nil {
		// addImage があれば、それに対して heatMap の画像を重ねた画像を作ります
		newImg := image.NewRGBA(image.Rect(0, 0, int(width), int(height)) )
		draw.Draw(newImg, newImg.Bounds(), image.Transparent, image.ZP, draw.Src)
		draw.Draw(newImg, image.Rect(0, 0, int(width), int(height)),
			*addImage, image.ZP, draw.Over)
		draw.Draw(newImg, image.Rect(0, 0, int(width), int(height)),
			img, image.ZP, draw.Over)
		img = newImg
	}

	png.Encode(imgFile, img)
	imgFile.Close()
	return nil
}

// 指定された秒だけ眺めるheatMapの画像を作って save します。
func SaveHeatMapImageSet(fileNameBase string, log *OneWebPageTrackLog, width int, height int, maxSecond int64, startUnixTime int64, heatMapImage *image.Image, backgroundImage *image.Image) error {
	// まずは素の eyetrack のデータを書き出します
	fileName := fmt.Sprintf("%s_%dsec.png", fileNameBase, maxSecond)
	err := SaveHeatMapImage(fileName, *log, width, height, maxSecond, startUnixTime, heatMapImage, nil)
	if err != nil {
		fmt.Printf("heatmap image create error: %q\n", err)
		return err
	}
	log.ImageFileNameList = append(log.ImageFileNameList, fileName)

	// backgroundImage があれば、その画像ファイルと合成した画像も作ります
	fileName = fmt.Sprintf("%s_bg_%dsec.png", fileNameBase, maxSecond)
	err = SaveHeatMapImage(fileName, *log, width, height, maxSecond, startUnixTime, heatMapImage, backgroundImage)
	if err != nil {
		fmt.Printf("heatmap image create error: %q\n", err)
		return err
	}
	log.ImageFileNameList = append(log.ImageFileNameList, fileName)
	return nil
}

func main(){
	var flags MainFlags
	flag.StringVar(&flags.LogFileName, "logFileName", "", "log file name")
	flag.StringVar(&flags.DirectoryName, "directoryName", time.Now().Format("20060102_030405"), "output directory name")
	flag.StringVar(&flags.ImageConfigFileName, "imageConfigFileName", "imageConfig.json", "image config file name (JSON format required)")
	flag.Parse()
	os.Args = flag.Args()

	logFileName := flags.LogFileName
	logFile, err := os.Open(logFileName)
	if err != nil {
		fmt.Printf("log file '%s' open error: %q\n", logFileName, err)
		return
	}

	imageConfig := LoadImageConfig(flags.ImageConfigFileName)
	
	reader := bufio.NewReaderSize(logFile, 20480)

	all_log := []OneWebPageTrackLog{}
	current_log := OneWebPageTrackLog{}
	current_log.Url = "UNKNOWN URL"

	// 一行づつ読み込みます
	for {
		bin, _, err := reader.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("read error: %q\n", err)
		}
		line := string(bin)
		if strings.Contains(line, "request path") {
			// URLをクリックした行
			var requestPath RequestPath
			err = json.Unmarshal(bin, &requestPath)
			if err != nil {
				fmt.Printf("json decode error: %q -> %s\n", err, line)
				continue
			}
			all_log = append(all_log, current_log)
			current_log = OneWebPageTrackLog{}
			current_log.Url = requestPath.RequestPath
			current_log.UnixTime = requestPath.UnixTime
		}else{
			// フレーム
			var responseMessage OneFrameMessage
			err = json.Unmarshal(bin, &responseMessage)
			if err != nil {
				fmt.Printf("json decode error: %q -> %s\n", err, line)
				continue
			}
			var ok bool
			frame, ok := responseMessage.Values["frame"]
			if ok == false {
				fmt.Printf("response has no frame field")
				return
			}
			current_log.FrameArray = append(current_log.FrameArray, frame)
		}
	}
	// 最後の分が入っていないはずなので入れます
	all_log = append(all_log, current_log)

	// ここまでで、all_log にそれぞれのページ毎のデータが入っているはず。

	// 問答無用に現在時刻でディレクトリを掘ります。
	dirName := flags.DirectoryName
	if dirName == "" {
		dirName = time.Now().Format("20060102_030405")
	}
	err = os.MkdirAll(dirName, os.ModeDir)
	if err != nil {
		fmt.Printf("directory %s create error: %q\n", dirName, err)
		return
	}

	fmt.Printf("log loaded. %d sites in data. create directory \"%s\" for heatmap images.\n",
		len(all_log), dirName)

	// heatMap の画像をそのディレクトリに作ります
	heatMapImage, err := LoadPngImage("heatmap_brush.png")
	if err != nil {
		fmt.Printf("heatmap_brush.png load error: %q\n",  err)
		return
	}
	for i := 0; i < len(all_log); i++ {
		log := &all_log[i]
		width := 1920
		height := 1080
		fileNameBase := fmt.Sprintf("%s/%d", dirName, i)

		var backgroundImage *image.Image
		backgroundImage = nil
		// imageConfig に定義されている名前のURLであれば、
		// その画像ファイルと合成した画像も作るために load しておきます
		imageFile := imageConfig[log.Url]
		if imageFile != "" {
			backgroundImage, err = LoadPngImage(imageFile)
			if err != nil {
				fmt.Printf("%s load error: %q. image file MUST need PNG file format.\n",  err)
				return
			}
		}

		// 最初は全てのもの
		err = SaveHeatMapImageSet(fileNameBase, log, width, height, -1, log.UnixTime, heatMapImage, backgroundImage)
		if err != nil {
			fmt.Printf("heatmap image create error: %q\n", err)
			return
		}
		// 5秒まで
		err = SaveHeatMapImageSet(fileNameBase, log, width, height, 5, log.UnixTime, heatMapImage, backgroundImage)
		if err != nil {
			fmt.Printf("heatmap image create error: %q\n", err)
			return
		}
		// 10秒まで
		err = SaveHeatMapImageSet(fileNameBase, log, width, height, 10, log.UnixTime, heatMapImage, backgroundImage)
		if err != nil {
			fmt.Printf("heatmap image create error: %q\n", err)
			return
		}
		// 15秒まで
		err = SaveHeatMapImageSet(fileNameBase, log, width, height, 15, log.UnixTime, heatMapImage, backgroundImage)
		if err != nil {
			fmt.Printf("heatmap image create error: %q\n", err)
			return
		}
	}
	
	fmt.Printf("  creating index.html\n")
	// heatMap 用の index.html を作ります
	indexFile, err := os.OpenFile(fmt.Sprintf("%s/index.html", dirName), os.O_CREATE|os.O_RDWR, 0666)
	if err != nil {
		fmt.Printf("heatmap index.html create error: %q\n", err)
		return
	}
	fmt.Fprintf(indexFile, "<html><head><title>heatmap: %s</title></head><body>", dirName)
	for i := 0; i < len(all_log); i++ {
		fmt.Fprintf(indexFile, "<hr>%s<br>", all_log[i].Url)
		log := all_log[i]
		for j := 0; j < len(log.ImageFileNameList); j++{
			fmt.Fprintf(indexFile, "<a href=\"../%s\"><img src=\"../%s\" width=\"100\"></a> ", log.ImageFileNameList[j], log.ImageFileNameList[j])
		}
	}
	fmt.Fprintf(indexFile, "</body></html>")
	indexFile.Close()
	fmt.Printf("done!\n")
}


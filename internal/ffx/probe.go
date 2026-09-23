package ffx

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Media es lo que importa de un archivo de video para planificar la compresión.
type Media struct {
	Path     string
	Duration time.Duration
	Size     int64
	BitRate  int64 // bits por segundo del contenedor
	Video    *VideoStream
	Audio    *AudioStream
}

// VideoStream describe la pista de video principal.
type VideoStream struct {
	Codec    string
	Width    int // ya con la rotación aplicada (lo que se ve en pantalla)
	Height   int
	FPS      float64
	Frames   int64
	PixFmt   string
	Transfer string // smpte2084 (PQ) o arib-std-b67 (HLG) = HDR
	Rotation int    // grados
	BitRate  int64
}

// HDR dice si el video usa una curva de transferencia HDR.
func (v *VideoStream) HDR() bool {
	return v.Transfer == "smpte2084" || v.Transfer == "arib-std-b67"
}

// AudioStream describe la pista de audio principal.
type AudioStream struct {
	Codec      string
	Channels   int
	SampleRate int
	BitRate    int64
}

type probeJSON struct {
	Format struct {
		Duration string `json:"duration"`
		Size     string `json:"size"`
		BitRate  string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		CodecType     string `json:"codec_type"`
		CodecName     string `json:"codec_name"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		RFrameRate    string `json:"r_frame_rate"`
		AvgFrameRate  string `json:"avg_frame_rate"`
		NbFrames      string `json:"nb_frames"`
		PixFmt        string `json:"pix_fmt"`
		ColorTransfer string `json:"color_transfer"`
		Channels      int    `json:"channels"`
		SampleRate    string `json:"sample_rate"`
		BitRate       string `json:"bit_rate"`
		Duration      string `json:"duration"`
		Disposition   struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
		Tags struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
}

// Probe lee las propiedades del archivo con ffprobe.
func (t Tools) Probe(ctx context.Context, path string) (*Media, error) {
	cmd := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("ffprobe no pudo leer el archivo: %s", lastLines(strings.TrimSpace(string(ee.Stderr)), 2))
		}
		return nil, fmt.Errorf("ffprobe no pudo leer el archivo: %w", err)
	}
	var pj probeJSON
	if err := json.Unmarshal(out, &pj); err != nil {
		return nil, fmt.Errorf("respuesta de ffprobe ilegible: %w", err)
	}
	m := &Media{Path: path}
	m.Duration = seconds(pj.Format.Duration)
	m.Size, _ = strconv.ParseInt(pj.Format.Size, 10, 64)
	m.BitRate, _ = strconv.ParseInt(pj.Format.BitRate, 10, 64)

	for _, s := range pj.Streams {
		switch {
		case s.CodecType == "video" && m.Video == nil && s.Disposition.AttachedPic == 0:
			v := &VideoStream{Codec: s.CodecName, Width: s.Width, Height: s.Height, PixFmt: s.PixFmt, Transfer: s.ColorTransfer}
			v.FPS = ratio(s.AvgFrameRate)
			if v.FPS <= 0 || v.FPS > 1000 {
				v.FPS = ratio(s.RFrameRate)
			}
			v.Frames, _ = strconv.ParseInt(s.NbFrames, 10, 64)
			v.BitRate, _ = strconv.ParseInt(s.BitRate, 10, 64)
			rot, _ := strconv.Atoi(s.Tags.Rotate)
			for _, sd := range s.SideData {
				if sd.Rotation != 0 {
					rot = int(math.Round(sd.Rotation))
				}
			}
			v.Rotation = ((rot % 360) + 360) % 360
			if v.Rotation == 90 || v.Rotation == 270 {
				v.Width, v.Height = v.Height, v.Width // ffmpeg rota solo al decodificar
			}
			if m.Duration == 0 {
				m.Duration = seconds(s.Duration)
			}
			m.Video = v
		case s.CodecType == "audio" && m.Audio == nil:
			a := &AudioStream{Codec: s.CodecName, Channels: s.Channels}
			a.SampleRate, _ = strconv.Atoi(s.SampleRate)
			a.BitRate, _ = strconv.ParseInt(s.BitRate, 10, 64)
			m.Audio = a
		}
	}
	if m.Duration <= 0 {
		return nil, fmt.Errorf("no pude saber cuánto dura el archivo")
	}
	return m, nil
}

func seconds(s string) time.Duration {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 || math.IsNaN(f) {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// ratio interpreta "30000/1001" → 29,97.
func ratio(s string) float64 {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	n, err1 := strconv.ParseFloat(num, 64)
	d, err2 := strconv.ParseFloat(den, 64)
	if err1 != nil || err2 != nil || d == 0 {
		return 0
	}
	return n / d
}

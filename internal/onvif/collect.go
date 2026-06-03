package onvif

import (
	"context"
	"encoding/xml"
)

// Profile is one media profile (a stream) on the camera.
type Profile struct {
	Token    string
	Name     string
	Width    int
	Height   int
	Encoding string // H264 | H265 | JPEG
}

// CameraInfo is the normalized ONVIF inventory for one camera.
type CameraInfo struct {
	Manufacturer string
	Model        string
	Firmware     string
	Serial       string
	HardwareID   string
	Profiles     []Profile
}

// Resolution returns "WxH" of the first profile, or "" if none.
func (c CameraInfo) Resolution() string {
	if len(c.Profiles) == 0 || c.Profiles[0].Width == 0 {
		return ""
	}
	return itoa(c.Profiles[0].Width) + "x" + itoa(c.Profiles[0].Height)
}

// --- response shapes (local-name matched, namespace-agnostic) ---------------

type deviceInfoResp struct {
	Manufacturer string `xml:"Body>GetDeviceInformationResponse>Manufacturer"`
	Model        string `xml:"Body>GetDeviceInformationResponse>Model"`
	Firmware     string `xml:"Body>GetDeviceInformationResponse>FirmwareVersion"`
	Serial       string `xml:"Body>GetDeviceInformationResponse>SerialNumber"`
	HardwareID   string `xml:"Body>GetDeviceInformationResponse>HardwareId"`
}

type rawProfile struct {
	Token    string `xml:"token,attr"`
	Name     string `xml:"Name"`
	Width    int    `xml:"VideoEncoderConfiguration>Resolution>Width"`
	Height   int    `xml:"VideoEncoderConfiguration>Resolution>Height"`
	Encoding string `xml:"VideoEncoderConfiguration>Encoding"`
}

type profilesResp struct {
	Profiles []rawProfile `xml:"Body>GetProfilesResponse>Profiles"`
}

const (
	nsDevice = `http://www.onvif.org/ver10/device/wsdl`
	nsMedia  = `http://www.onvif.org/ver10/media/wsdl`
)

// Collect fetches device information (required) + media profiles (best-effort).
func Collect(ctx context.Context, c *Client) (CameraInfo, error) {
	devBody, err := c.call(ctx, "/onvif/device_service", `<GetDeviceInformation xmlns="`+nsDevice+`"/>`)
	if err != nil {
		return CameraInfo{}, err
	}
	var di deviceInfoResp
	if err := xml.Unmarshal(devBody, &di); err != nil {
		return CameraInfo{}, err
	}
	info := CameraInfo{
		Manufacturer: di.Manufacturer, Model: di.Model, Firmware: di.Firmware,
		Serial: di.Serial, HardwareID: di.HardwareID,
	}

	// Profiles are best-effort: a camera may restrict the media service.
	if profBody, err := c.call(ctx, "/onvif/Media", `<GetProfiles xmlns="`+nsMedia+`"/>`); err == nil {
		var pr profilesResp
		if xml.Unmarshal(profBody, &pr) == nil {
			for _, p := range pr.Profiles {
				info.Profiles = append(info.Profiles, Profile{
					Token: p.Token, Name: p.Name, Width: p.Width, Height: p.Height, Encoding: p.Encoding,
				})
			}
		}
	}
	return info, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

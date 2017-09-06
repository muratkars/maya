package jiva

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/openebs/maya/command"
)

var (
	MaximumVolumeNameSize = 64
	parsePattern          = regexp.MustCompile(`(.*):(\d+)`)
)

func Snapshot(volname string, snapname string, labels map[string]string) (string, error) {

	annotations, err := command.GetVolumeSpec(volname)
	if err != nil || annotations == nil {

		return "", err
	}

	if annotations.ControllerStatus != "Running" {
		fmt.Println("Volume not reachable")
		return "", err
	}
	controller, err := command.NewControllerClient(annotations.ControllerIP + ":9501")

	if err != nil {
		return "", err
	}

	volume, err := command.GetVolume(controller.Address)
	if err != nil {
		return "", err
	}

	url := controller.Address + "/volumes/" + volume.Id + "?action=snapshot"

	fmt.Println("Url is:", url)

	input := command.SnapshotInput{
		Name:   snapname,
		Labels: labels,
	}
	output := command.SnapshotOutput{}
	var c ControllerClient
	err = c.post(url, input, &output)
	if err != nil {
		return "", err
	}

	return output.Id, err
}

func (c *ControllerClient) post(path string, req, resp interface{}) error {
	return c.do("POST", path, req, resp)
}

func (c *ControllerClient) do(method, path string, req, resp interface{}) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}

	bodyType := "application/json"
	url := path
	if !strings.HasPrefix(url, "http") {
		url = c.Address + path

	}
	fmt.Println("Do url:", url)
	httpReq, err := http.NewRequest(method, url, bytes.NewBuffer(b))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", bodyType)

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode >= 300 {
		content, _ := ioutil.ReadAll(httpResp.Body)
		return fmt.Errorf("Bad response: %d %s: %s", httpResp.StatusCode, httpResp.Status, content)
	}

	if resp == nil {
		return nil
	}
	return json.NewDecoder(httpResp.Body).Decode(resp)
}

/*func GetVolume(path string) (*Volumes, error) {
	var volume VolumeCollection
	var c ControllerClient

	err := c.get(path+"/volumes", &volume)
	if err != nil {
		return nil, err
	}

	if len(volume.Data) == 0 {
		return nil, errors.New("No volume found")
	}

	return &volume.Data[0], nil
}*/
func (c *ControllerClient) get(path string, obj interface{}) error {
	//	resp, err := http.Get(c.address + path)
	resp, err := http.Get(path)

	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return json.NewDecoder(resp.Body).Decode(obj)
}

/*
func lookupStringSlice(name string, set *flag.FlagSet) []string {
	f := set.Lookup(name)
	if f != nil {
		return (f.Value.(*StringSlice)).Value()

	}

	return nil
}
*/

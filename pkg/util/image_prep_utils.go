/*
Copyright 2017 Google, Inc. All rights reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"archive/tar"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"sync"

	"github.com/containers/image/pkg/compression"
	"github.com/containers/image/types"
	"github.com/golang/glog"
)

type Prepper interface {
	Name() string
	GetConfig() (ConfigSchema, error)
	GetFileSystem() (string, error)
	GetImage() (Image, error)
	GetSource() string
}

type Image struct {
	Source string
	FSPath string
	Config ConfigSchema
}

type ImageHistoryItem struct {
	CreatedBy string `json:"created_by"`
}

type ConfigObject struct {
	Env []string `json:"Env"`
}

type ConfigSchema struct {
	Config  ConfigObject       `json:"config"`
	History []ImageHistoryItem `json:"history"`
}

func getImage(p Prepper) (Image, error) {
	glog.Infof("Retrieving image %s from source %s", p.GetSource(), p.Name())
	imgPath, err := p.GetFileSystem()
	if err != nil {
		return Image{}, err
	}

	config, err := p.GetConfig()
	if err != nil {
		glog.Error("Error retrieving History: ", err)
	}

	glog.Infof("Finished prepping image %s", p.GetSource())
	return Image{
		Source: p.GetSource(),
		FSPath: imgPath,
		Config: config,
	}, nil
}

func getImageFromTar(tarPath string) (string, error) {
	glog.Info("Extracting image tar to obtain image file system")
	tempPath, err := ioutil.TempDir("", ".container-diff")
	if err != nil {
		return "", err
	}
	return tempPath, unpackDockerSave(tarPath, tempPath)
}

func getFileSystemFromReference(ref types.ImageReference, imageName string) (string, error) {
	sanitizedName := strings.Replace(imageName, ":", "", -1)
	sanitizedName = strings.Replace(sanitizedName, "/", "", -1)

	path, err := ioutil.TempDir("", sanitizedName)
	if err != nil {
		return "", err
	}

	img, err := ref.NewImage(nil)
	if err != nil {
		glog.Error(err)
		return "", err
	}
	defer img.Close()

	imgSrc, err := ref.NewImageSource(nil, nil)
	if err != nil {
		glog.Error(err)
		return "", err
	}

	err = processLayers(path, imgSrc, img.LayerInfos())
	return path, err
}

func processLayers(path string, imgSrc types.ImageSource, layerInfos []types.BlobInfo) error {
	errs := make(chan error)
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(layerInfos))
		for i, b := range layerInfos {
			go func(b types.BlobInfo, i int) {
				defer wg.Done()
				bi, _, err := imgSrc.GetBlob(b)
				if err != nil {
					errs <- fmt.Errorf("Failed to pull image layer: %s", err)
					return
				}
				// try and detect layer compression
				f, reader, err := compression.DetectCompression(bi)
				if err != nil {
					errs <- fmt.Errorf("Failed to detect image compression: %s", err)
					return
				}
				if f != nil {
					// decompress if necessary
					reader, err = f(reader)
					if err != nil {
						errs <- fmt.Errorf("Failed to decompress image: %s", err)
						return
					}
				}
				tr := tar.NewReader(reader)
				err = unpackTar(tr, path)
				if err != nil {
					errs <- fmt.Errorf("Failed to untar layer with error: %s", err)
				}
				return
			}(b, i)
		}
		wg.Wait()
		close(errs)
	}()
	var err error
	if len(errs) != 0 {
		var fullError string
		for err := range errs {
			fullError = fullError + err.Error()
		}
		return errors.New(fullError)
	}
	return err
}

func getConfigFromReference(ref types.ImageReference, source string) (ConfigSchema, error) {
	img, err := ref.NewImage(nil)
	if err != nil {
		glog.Errorf("Error referencing image %s from registry: %s", source, err)
		return ConfigSchema{}, errors.New("Could not obtain image config")
	}
	defer img.Close()

	configBlob, err := img.ConfigBlob()
	if err != nil {
		glog.Errorf("Error obtaining config blob for image %s from registry: %s", source, err)
		return ConfigSchema{}, errors.New("Could not obtain image config")
	}

	var config ConfigSchema
	err = json.Unmarshal(configBlob, &config)
	if err != nil {
		glog.Errorf("Error with config file struct for image %s: %s", source, err)
		return ConfigSchema{}, errors.New("Could not obtain image config")
	}
	return config, nil
}

func CleanupImage(image Image) {
	if image.FSPath != "" {
		glog.Infof("Removing image filesystem directory %s from system", image.FSPath)
		if err := os.RemoveAll(image.FSPath); err != nil {
			glog.Error(err.Error())
		}
	}
}

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
	"errors"
	"fmt"
	"strings"

	"github.com/docker/docker/client"
	"github.com/golang/glog"
)

type ImagePrepper struct {
	Source string
	Client *client.Client
}

type Prepper interface {
	Name() string
	GetSource() string
	GetFileSystem() (string, error)
	GetConfig() (ConfigSchema, error)
	SupportsImage() bool
}

func getImage(prepper Prepper) (Image, error) {
	imgPath, err := prepper.GetFileSystem()
	if err != nil {
		return Image{}, fmt.Errorf("error msg: %s", err.Error())
	}

	config, err := prepper.GetConfig()
	if err != nil {
		return Image{}, fmt.Errorf("error msg: %s", err.Error())
	}

	glog.Infof("Finished prepping image %s", prepper.GetSource())
	return Image{
		Source: prepper.GetSource(),
		FSPath: imgPath,
		Config: config,
	}, nil
}

func (p *ImagePrepper) GetImage() (Image, error) {
	glog.Infof("Starting prep for image %s", p.Source)

	var prepper Prepper

	// first, respect prefixes on image names
	if strings.HasPrefix(p.Source, DaemonPrefix) {
		p.Source = strings.Replace(p.Source, DaemonPrefix, "", -1)
		prepper = DaemonPrepper{ImagePrepper: p}
		return getImage(prepper)
	} else if strings.HasPrefix(p.Source, RemotePrefix) {
		p.Source = strings.Replace(p.Source, RemotePrefix, "", -1)
		prepper = CloudPrepper{ImagePrepper: p}
		return getImage(prepper)
	}

	// if no prefix found, check local daemon first, otherwise default to cloud registry
	for _, prepperConstructor := range orderedPreppers {
		prepper = prepperConstructor(p)
		if prepper.SupportsImage() {
			glog.Infof("Attempting to retrieve image from %s", prepper.Name())

			img, err := getImage(prepper)
			if err != nil {
				glog.Warning(err.Error())
				continue
			}
			return img, nil
		}
	}
	return Image{}, errors.New("Could not retrieve image from source")
}

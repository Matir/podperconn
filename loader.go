// Copyright 2023 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package podpercon

import (
	"bytes"
	"io/ioutil"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
)

type LoadedObject struct {
	GroupVersionKind schema.GroupVersionKind
	Object           runtime.Object
}

func LoadObjectsFromFile(fname string) ([]LoadedObject, error) {
	data, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, err
	}
	return LoadObjectsFromBytes(data)
}

// Inspired by https://dx13.co.uk/articles/2021/01/15/kubernetes-types-using-go/
func LoadObjectsFromBytes(data []byte) ([]LoadedObject, error) {
	docs := splitDocuments(data)
	decoder := scheme.Codecs.UniversalDeserializer()
	res := make([]LoadedObject, 0)
	for _, doc := range docs {
		obj, groupVersionKind, err := decoder.Decode(doc, nil, nil)
		if err != nil {
			logger.WithError(err).Errorf("Failed decoding object.")
			return nil, err
		}
		item := LoadedObject{
			GroupVersionKind: *groupVersionKind,
			Object:           obj,
		}
		res = append(res, item)
	}
	return res, nil
}

// Splits documents separated by ---
// Probably doesn't handle edge cases like two consecutive separators well
func splitDocuments(data []byte) [][]byte {
	if bytes.HasPrefix(data, []byte("---\n")) {
		data = data[4:]
	}
	if bytes.HasSuffix(data, []byte("\n---")) {
		data = data[:len(data)-4]
	}
	docs := bytes.Split(data, []byte("\n---\n"))
	res := make([][]byte, 0, len(docs))
	for _, d := range docs {
		if len(d) > 0 {
			res = append(res, d)
		}
	}
	return res
}

// Converts LoadedObject to Deployment, or nil if not a deployment
func (lo *LoadedObject) GetDeployment() *appsv1.Deployment {
	if lo.GroupVersionKind.Group == "apps" &&
		lo.GroupVersionKind.Version == "v1" &&
		lo.GroupVersionKind.Kind == "Deployment" {
		deployment, ok := lo.Object.(*appsv1.Deployment)
		if ok {
			return deployment
		}
		logger.Errorf("Expected apps/v1/Deployment, but type conversion failed.")
	}
	return nil
}

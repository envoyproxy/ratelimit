// Copyright 2020 Envoyproxy Authors
//
//   Licensed under the Apache License, Version 2.0 (the "License");
//   you may not use this file except in compliance with the License.
//   You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
//   Unless required by applicable law or agreed to in writing, software
//   distributed under the License is distributed on an "AS IS" BASIS,
//   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//   See the License for the specific language governing permissions and
//   limitations under the License.
package example

import (
	"log"
)

type Logger struct {
	Debug bool
}

func (logger Logger) Debugf(format string, args ...interface{}) {
	if logger.Debug {
		log.Printf("[DEBUG] "+format+"\n", args...)
	}
}

func (logger Logger) Infof(format string, args ...interface{}) {
	log.Printf("[INFO]"+format+"\n", args...)
}

func (logger Logger) Warnf(format string, args ...interface{}) {
	log.Printf("[WARN] "+format+"\n", args...)
}

func (logger Logger) Errorf(format string, args ...interface{}) {
	log.Printf("[ERROR]"+format+"\n", args...)
}

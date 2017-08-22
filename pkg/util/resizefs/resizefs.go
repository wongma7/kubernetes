// +build linux

/*
Copyright 2017 The Kubernetes Authors.

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

package resizefs

import (
	"fmt"
	"os/exec"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

func ResizeExt(device string, size *resource.Quantity) error {
	cmd := exec.Command("resize2fs", device)
	if size != nil {
		size.Format = resource.DecimalSI
		cmd.Args = append(cmd.Args, size.String())
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("'resize2fs' on path %s failed: %v\n Output: %s\n", device, err, string(out))
	}

	// resize2fs may output the following line:
	// "Please run 'e2fsck -f $DEVICE' first."
	if strings.Contains(string(out), "Please run 'e2fsck -f") {
		// Add -y to non-interactively answer "yes" to everything
		e2fsckCmd := exec.Command("e2fsck", "-f", "-y", device)
		out, err := e2fsckCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("'e2fsck -f' on path %s failed: %v\n Output: %s\n", device, err, string(out))
		}

		// Retry resize2fs after successful e2fsck -f
		resize2fsCmd := exec.Command("resize2fs", device)
		out, err = resize2fsCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("'resize2fs' on path %s failed: %v\n Output: %s\n", device, err, string(out))
		}
	}

	return nil
}

/*
Copyright 2016 The Kubernetes Authors.

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

package volumemanager

import (
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	"k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset/fake"
	containertest "k8s.io/kubernetes/pkg/kubelet/container/testing"
	"k8s.io/kubernetes/pkg/kubelet/pod"
	kubepod "k8s.io/kubernetes/pkg/kubelet/pod"
	podtest "k8s.io/kubernetes/pkg/kubelet/pod/testing"
	utiltesting "k8s.io/kubernetes/pkg/util/testing"
	"k8s.io/kubernetes/pkg/volume"
	volumetest "k8s.io/kubernetes/pkg/volume/testing"
	"k8s.io/kubernetes/pkg/volume/util/volumehelper"
)

const (
	testHostname = "test-hostname"
)

func TestGetExtraSupplementalGroupsForPod(t *testing.T) {
	tmpDir, err := utiltesting.MkTmpdir("volumeManagerTest")
	if err != nil {
		t.Fatalf("can't make a temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	podManager := kubepod.NewBasicPodManager(podtest.NewFakeMirrorClient())

	expected := []int64{666}

	pod := &api.Pod{
		ObjectMeta: api.ObjectMeta{
			Name:      "abc",
			Namespace: "nsA",
		},
		Spec: api.PodSpec{
			Volumes: []api.Volume{
				{
					Name: "vol1",
					VolumeSource: api.VolumeSource{
						PersistentVolumeClaim: &api.PersistentVolumeClaimVolumeSource{
							ClaimName: "claimA",
						},
					},
				},
			},
			SecurityContext: &api.PodSecurityContext{
				SupplementalGroups: []int64{555},
			},
		},
	}
	pv := &api.PersistentVolume{
		ObjectMeta: api.ObjectMeta{
			Name: "pvA",
			Annotations: map[string]string{
				volumehelper.VolumeGidAnnotationKey: strconv.FormatInt(expected[0], 10),
			},
		},
		Spec: api.PersistentVolumeSpec{
			PersistentVolumeSource: api.PersistentVolumeSource{
				GCEPersistentDisk: &api.GCEPersistentDiskVolumeSource{
					PDName: "fake-device",
				},
			},
			ClaimRef: &api.ObjectReference{
				Name: "claimA",
			},
		},
	}
	claim := &api.PersistentVolumeClaim{
		ObjectMeta: api.ObjectMeta{
			Name:      "claimA",
			Namespace: "nsA",
		},
		Spec: api.PersistentVolumeClaimSpec{
			VolumeName: "pvA",
		},
		Status: api.PersistentVolumeClaimStatus{
			Phase: api.ClaimBound,
		},
	}
	node := &api.Node{
		ObjectMeta: api.ObjectMeta{Name: testHostname},
		Status: api.NodeStatus{
			VolumesAttached: []api.AttachedVolume{
				{
					Name:       "fake/pvA",
					DevicePath: "fake/path",
				},
			}},
		Spec: api.NodeSpec{ExternalID: testHostname},
	}

	kubeClient := fake.NewSimpleClientset(pod, pv, claim, node)

	manager, err := newTestVolumeManager(tmpDir, podManager, kubeClient)
	if err != nil {
		t.Fatalf("Failed to initialize volume manager: %v", err)
	}

	stopCh := make(chan struct{})
	go manager.Run(stopCh)
	defer func() {
		close(stopCh)
	}()

	podManager.SetPods([]*api.Pod{pod})

	go simulateVolumeInUseUpdate(
		api.UniqueVolumeName("fake/pvA"),
		stopCh,
		manager)

	err = manager.WaitForAttachAndMount(pod)
	if err != nil {
		t.Errorf("Expected success: %v", err)
	}

	actual := manager.GetExtraSupplementalGroupsForPod(pod)
	if !reflect.DeepEqual(expected, actual) {
		t.Errorf("Expected supplemental groups %v, got %v", expected, actual)
	}
}

func newTestVolumeManager(
	tmpDir string,
	podManager pod.Manager,
	kubeClient internalclientset.Interface) (VolumeManager, error) {
	plug := &volumetest.FakeVolumePlugin{PluginName: "fake", Host: nil}
	plugMgr := &volume.VolumePluginMgr{}
	plugMgr.InitPlugins([]volume.VolumePlugin{plug}, volumetest.NewFakeVolumeHost(tmpDir, kubeClient, nil, "" /* rootContext */))

	vm, err := NewVolumeManager(
		true,
		testHostname,
		podManager,
		kubeClient,
		plugMgr,
		&containertest.FakeRuntime{})
	return vm, err
}

func simulateVolumeInUseUpdate(
	volumeName api.UniqueVolumeName,
	stopCh <-chan struct{},
	volumeManager VolumeManager) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			volumeManager.MarkVolumesAsReportedInUse(
				[]api.UniqueVolumeName{volumeName})
		case <-stopCh:
			return
		}
	}
}

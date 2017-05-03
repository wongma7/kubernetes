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

package storage

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/kubernetes/pkg/api/v1"
	//extensions "k8s.io/kubernetes/pkg/apis/extensions/v1beta1"
	"k8s.io/kubernetes/pkg/client/clientset_generated/clientset"
	"k8s.io/kubernetes/test/e2e/framework"
)

// Testing configurations of single a PV/PVC pair attached to an EBS volume
var _ = framework.KubeDescribe("ASDF PersistentVolumes:AWS [Volume]", func() {
	var (
		c         clientset.Interface
		ns        string
		diskNames map[string]types.NodeName
		pvs       []*v1.PersistentVolume
		pvcs      []*v1.PersistentVolumeClaim
		//clientDeployments []*extensions.Deployment
		clientPods []*v1.Pod
	)
	const (
		numPods = 1
	)

	f := framework.NewDefaultFramework("pv-stress")
	BeforeEach(func() {
		framework.SkipUnlessProviderIs("aws")
		c = f.ClientSet
		ns = f.Namespace.Name
		diskNames = make(map[string]types.NodeName, numPods)
		pvs = make([]*v1.PersistentVolume, numPods)
		pvcs = make([]*v1.PersistentVolumeClaim, numPods)
		//clientDeployments = make([]*extensions.Deployment, 10)
		clientPods = make([]*v1.Pod, numPods)

		// Enforce binding only within test space via selector labels
		volLabel := labels.Set{framework.VolumeSelectorKey: ns}
		selector := metav1.SetAsLabelSelector(volLabel)

		for i := 0; i < numPods; i++ {
			diskName, err := framework.CreatePDWithRetry()
			Expect(err).NotTo(HaveOccurred())
			pvConfig := framework.PersistentVolumeConfig{
				NamePrefix: "aws-",
				Labels:     volLabel,
				PVSource: v1.PersistentVolumeSource{
					AWSElasticBlockStore: &v1.AWSElasticBlockStoreVolumeSource{
						VolumeID: diskName,
						FSType:   "ext3",
						ReadOnly: false,
					},
				},
				Prebind: nil,
			}
			pvcConfig := framework.PersistentVolumeClaimConfig{
				Annotations: map[string]string{
					v1.BetaStorageClassAnnotation: "",
				},
				Selector: selector,
			}
			// TODO use createpvs
			By("Creating the PV and PVC")
			pv, pvc, err := framework.CreatePVPVC(c, pvConfig, pvcConfig, ns, true)
			Expect(err).NotTo(HaveOccurred())
			framework.ExpectNoError(framework.WaitOnPVandPVC(c, ns, pv, pvc))

			By("Creating the Client Pod")
			// TODO  do in parallel
			clientPod, err := framework.CreateClientPod(c, ns, pvc)
			Expect(err).NotTo(HaveOccurred())
			node := types.NodeName(clientPod.Spec.NodeName)

			diskNames[diskName] = node
			pvs[i] = pv
			pvcs[i] = pvc
			clientPods[i] = clientPod
		}
	})

	AfterEach(func() {
		framework.Logf("AfterEach: Cleaning up test resources")
		if c != nil {
			for i := 0; i < numPods; i++ {
				//framework.ExpectNoError(framework.DeleteDeploymentWithWait(c, clientDeployments[i]))
				if errs := framework.PVPVCCleanup(c, ns, pvs[i], pvcs[i]); len(errs) > 0 {
					framework.Failf("AfterEach: Failed to delete PVC and/or PV. Errors: %v", utilerrors.NewAggregate(errs))
				}
				//clientDeployments, pvs, pvcs = nil, nil, nil
				clientPods, pvs, pvcs = nil, nil, nil

			}
			for diskName := range diskNames {
				framework.ExpectNoError(framework.DeletePDWithRetry(diskName))
			}
			diskNames = nil
		}
	})

	It("should detach a volume after both kubelet & controller-manager are restarted and the pod using the volume is deleted", func() {

		By("Restarting controller-manager")
		framework.ExpectNoError(framework.RestartControllerManager())

		By("Restarting kubelets")
		hosts, err := framework.NodeSSHHosts(c)
		framework.ExpectNoError(err)
		for _, host := range hosts {
			framework.ExpectNoError(framework.RestartKubelet(host))
		}

		//for _, d := range clientDeployments {
		//	framework.ExpectNoError(framework.ScaleDeployment(c, f.InternalClientset, ns, d.Name, 0, true))
		//}

		By("Deleting the Pods")
		for _, pod := range clientPods {
			framework.ExpectNoError(framework.DeletePodWithWait(f, c, pod), "Failed to delete pod ", pod.Name)
		}

		By("Waiting for controller-manager to come up")
		framework.ExpectNoError(framework.WaitForControllerManagerUp())

		By("Waiting for kubelets to come up")
		for _, host := range hosts {
			framework.ExpectNoError(framework.WaitForKubeletUp(host))
		}

		By("Verifying Persistent Disk detach")
		for diskName, nodeName := range diskNames {
			framework.ExpectNoError(waitForPDDetach(diskName, nodeName), "EBS ", diskName, " did not detach")
		}
	})
})

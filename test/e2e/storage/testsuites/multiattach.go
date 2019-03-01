/*
Copyright 2019 The Kubernetes Authors.

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

package testsuites

import (
	"math/rand"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/storage/testpatterns"
	"k8s.io/kubernetes/test/e2e/storage/utils"
)

type multiAttachTestSuite struct {
	tsInfo TestSuiteInfo
}

var _ TestSuite = &multiAttachTestSuite{}

// InitMultiAttachTestSuite returns multiAttachTestSuite that implements TestSuite interface
func InitMultiAttachTestSuite() TestSuite {
	return &multiAttachTestSuite{
		tsInfo: TestSuiteInfo{
			name: "multiAttach",
			testPatterns: []testpatterns.TestPattern{
				testpatterns.FsVolModeDynamicPV,
				testpatterns.BlockVolModeDynamicPV,
				testpatterns.FsVolModePreprovisionedPV,
				testpatterns.BlockVolModePreprovisionedPV,
				// Currently, multiple volumes are not generally available for pre-provisoined volume,
				// because containerized storage servers, such as iSCSI and rbd, are just returning
				// a static volume inside container, not actually creating a new volume per request.
				// So, only dynamic provision tests are defined for now.
			},
		},
	}
}

func (t *multiAttachTestSuite) getTestSuiteInfo() TestSuiteInfo {
	return t.tsInfo
}

func (t *multiAttachTestSuite) defineTests(driver TestDriver, pattern testpatterns.TestPattern) {
	type local struct {
		config      *PerTestConfig
		testCleanup func()

		cs clientset.Interface
		ns *v1.Namespace
		// resource for 1st volume
		resource1 *genericVolumeTestResource
		// resource for 2nd volume
		resource2 *genericVolumeTestResource
	}
	var (
		dInfo = driver.GetDriverInfo()
		l     local
	)

	BeforeEach(func() {
		// Check preconditions.
		if dInfo.Name != "iscsi" {
			if pattern.VolType != testpatterns.DynamicPV {
				framework.Skipf("Suite %q does not support %v", t.tsInfo.name, pattern.VolType)
			}
			_, ok := driver.(DynamicPVTestDriver)
			if !ok {
				framework.Skipf("Driver %s doesn't support %v -- skipping", dInfo.Name, pattern.VolType)
			}
			if pattern.VolMode == v1.PersistentVolumeBlock && !dInfo.Capabilities[CapBlock] {
				framework.Skipf("Driver %s doesn't support %v -- skipping", dInfo.Name, pattern.VolMode)
			}
		} else {
			if pattern.VolType != testpatterns.PreprovisionedPV {
				framework.Skipf("Suite %q does not support %v", t.tsInfo.name, pattern.VolType)
			}
			_, ok := driver.(PreprovisionedPVTestDriver)
			if !ok {
				framework.Failf("Expected driver %s to support %v", dInfo.Name, pattern.VolType)
			}
			if pattern.VolMode == v1.PersistentVolumeBlock && !dInfo.Capabilities[CapBlock] {
				framework.Failf("Expected driver %s to support %v", dInfo.Name, pattern.VolMode)
			}
		}
	})

	// This intentionally comes after checking the preconditions because it
	// registers its own BeforeEach which creates the namespace. Beware that it
	// also registers an AfterEach which renders f unusable. Any code using
	// f must run inside an It or Context callback.
	f := framework.NewDefaultFramework("multiattach")

	init := func() {
		l = local{}
		l.ns = f.Namespace
		l.cs = f.ClientSet

		// Now do the more expensive test initialization.
		l.config, l.testCleanup = driver.PrepareTest(f)

		l.resource1 = createGenericVolumeTestResource(driver, l.config, pattern)
		l.resource2 = &genericVolumeTestResource{
			driver:  driver,
			config:  l.config,
			pattern: pattern,
		}
	}

	cleanup := func() {
		if l.resource1 != nil {
			l.resource1.cleanupResource()
			l.resource1 = nil
		}

		if l.resource2 != nil {
			l.resource2.cleanupResource()
			l.resource2 = nil
		}

		if l.testCleanup != nil {
			l.testCleanup()
			l.testCleanup = nil
		}
	}

	testAttachTwoVolumesToOnePod := func(l local) {
		var err error

		l.resource2.sc = l.resource1.sc

		if dDriver, ok := l.resource2.driver.(DynamicPVTestDriver); ok {
			claimSize := dDriver.GetClaimSize()
			l.resource2.pvc = getClaim(claimSize, l.ns.Name)
			l.resource2.pvc.Spec.StorageClassName = &l.resource2.sc.Name
			l.resource2.pvc.Spec.VolumeMode = &l.resource2.pattern.VolMode
		} else {
			framework.Skipf("Driver %q does not define Dynamic Provision StorageClass - skipping", l.resource2.driver.GetDriverInfo().Name)
		}

		By("Creating pv and pvc")
		l.resource2.pvc, err = l.cs.CoreV1().PersistentVolumeClaims(l.ns.Name).Create(l.resource2.pvc)
		Expect(err).NotTo(HaveOccurred())

		err = framework.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, l.cs, l.resource2.pvc.Namespace, l.resource2.pvc.Name, framework.Poll, framework.ClaimProvisionTimeout)
		Expect(err).NotTo(HaveOccurred())

		l.resource2.pvc, err = l.cs.CoreV1().PersistentVolumeClaims(l.resource2.pvc.Namespace).Get(l.resource2.pvc.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		l.resource2.pv, err = l.cs.CoreV1().PersistentVolumes().Get(l.resource2.pvc.Spec.VolumeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("Creating pod with the two volumes")
		pod, err := framework.CreateSecPodWithNodeName(l.cs, l.ns.Name,
			[]*v1.PersistentVolumeClaim{l.resource1.pvc, l.resource2.pvc},
			false, "", false, false, framework.SELinuxLabel,
			nil, l.config.ClientNodeName, framework.PodStartTimeout)
		defer func() {
			framework.ExpectNoError(framework.DeletePodWithWait(f, l.cs, pod))
		}()
		Expect(err).NotTo(HaveOccurred())

		By("Checking if the 1st volume exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod, l.resource1.pattern.VolMode, "/mnt/volume1")

		By("Checking if read/write to the 1st volume works properly")
		utils.CheckReadWriteToPath(pod, l.resource1.pattern.VolMode, "/mnt/volume1")

		By("Checking if the 2nd volume exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod, l.resource2.pattern.VolMode, "/mnt/volume2")

		By("Checking if read/write to the 2nd volume works properly")
		utils.CheckReadWriteToPath(pod, l.resource2.pattern.VolMode, "/mnt/volume2")
	}

	// This tests below configuration:
	//          [pod1]
	//          /    \      <- same volume mode
	//   [volume1]  [volume2]
	It("should attach two volumes with the same volume mode to the same pod", func() {
		init()
		defer cleanup()

		l.resource2.pattern.VolMode = l.resource1.pattern.VolMode
		testAttachTwoVolumesToOnePod(l)
	})

	// This tests below configuration:
	//          [pod1]
	//          /    \      <- different volume mode (only <block, filesystem> pattern is tested)
	//   [volume1]  [volume2]
	It("should attach two volumes with different volume mode to the same pod", func() {
		if pattern.VolMode == v1.PersistentVolumeFilesystem {
			framework.Skipf("Filesystem volume case should be covered by block volume case -- skipping")
		}

		init()
		defer cleanup()

		l.resource2.pattern.VolMode = v1.PersistentVolumeBlock
		testAttachTwoVolumesToOnePod(l)
	})

	// This tests below configuration:
	// [pod1] [pod2]
	// [   node1   ]
	//   \      /     <- same volume mode
	//   [volume1]
	It("should attach the same volume with the same volume mode to the different pod on the same node", func() {
		init()
		defer cleanup()

		var err error

		// If node is not specified by l.config.ClientNodeName,
		// decide the node to deploy both 1st pod and 2nd pod
		// so that they will be deployed on the same node.
		nodeName := l.config.ClientNodeName
		if nodeName == "" {
			nodes := framework.GetReadySchedulableNodesOrDie(l.cs)
			nodeName = nodes.Items[rand.Intn(len(nodes.Items))].Name
		}

		By("Creating 1st pod with a volume")
		pod1, err := framework.CreateSecPodWithNodeName(l.cs, l.ns.Name,
			[]*v1.PersistentVolumeClaim{l.resource1.pvc},
			false, "", false, false, framework.SELinuxLabel,
			nil, nodeName, framework.PodStartTimeout)
		defer func() {
			framework.ExpectNoError(framework.DeletePodWithWait(f, l.cs, pod1))
		}()
		Expect(err).NotTo(HaveOccurred())

		By("Creating 2nd pod with the same volume")
		pod2, err := framework.CreateSecPodWithNodeName(l.cs, l.ns.Name,
			[]*v1.PersistentVolumeClaim{l.resource1.pvc},
			false, "", false, false, framework.SELinuxLabel,
			nil, nodeName, framework.PodStartTimeout)
		defer func() {
			framework.ExpectNoError(framework.DeletePodWithWait(f, l.cs, pod2))
		}()
		Expect(err).NotTo(HaveOccurred())

		By("Checking if the volume in 1st pod exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod1, pattern.VolMode, "/mnt/volume1")

		By("Checking if read/write to the volume in 1st pod works properly")
		utils.CheckReadWriteToPath(pod1, pattern.VolMode, "/mnt/volume1")

		By("Checking if the volume in 2nd pod exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod2, pattern.VolMode, "/mnt/volume1")

		By("Checking if read/write to the volume in 2nd pod works properly")
		utils.CheckReadWriteToPath(pod2, pattern.VolMode, "/mnt/volume1")

		// TODO: Delete one of the pod, then test read/write work well in ther other pod

	})
	testAttachOneVolumeToTwoPods := func(l local) {
		var err error

		l.resource2.sc = l.resource1.sc

		pv := &v1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: l.ns.Name,
			},
			Spec: l.resource1.pv.Spec,
		}
		pv.Spec.ClaimRef = nil
		// iscsi-only
		Expect(pv.Spec.PersistentVolumeSource.ISCSI).NotTo(BeNil())
		pv.Spec.PersistentVolumeSource.ISCSI.Lun = 1
		//
		l.resource2.pv = pv

		l.resource2.pvc = getClaim("1Gi", l.ns.Name)
		l.resource2.pvc.Spec.StorageClassName = &l.resource2.sc.Name
		l.resource2.pvc.Spec.VolumeMode = &l.resource2.pattern.VolMode
		l.resource2.pvc.Spec.VolumeName = l.resource2.pv.Name

		By("Creating pv and pvc")
		l.resource2.pv, err = l.cs.CoreV1().PersistentVolumes().Create(l.resource2.pv)
		Expect(err).NotTo(HaveOccurred())

		l.resource2.pvc, err = l.cs.CoreV1().PersistentVolumeClaims(l.ns.Name).Create(l.resource2.pvc)
		Expect(err).NotTo(HaveOccurred())

		err = framework.WaitForPersistentVolumeClaimPhase(v1.ClaimBound, l.cs, l.resource2.pvc.Namespace, l.resource2.pvc.Name, framework.Poll, framework.ClaimProvisionTimeout)
		Expect(err).NotTo(HaveOccurred())

		l.resource2.pvc, err = l.cs.CoreV1().PersistentVolumeClaims(l.resource2.pvc.Namespace).Get(l.resource2.pvc.Name, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		l.resource2.pv, err = l.cs.CoreV1().PersistentVolumes().Get(l.resource2.pvc.Spec.VolumeName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())

		// If node is not specified by l.config.ClientNodeName,
		// decide the node to deploy both 1st pod and 2nd pod
		// so that they will be deployed on the same node.
		nodeName := l.config.ClientNodeName
		if nodeName == "" {
			nodes := framework.GetReadySchedulableNodesOrDie(l.cs)
			nodeName = nodes.Items[rand.Intn(len(nodes.Items))].Name
		}

		By("Creating 1st pod with a volume")
		pod1, err := framework.CreateSecPodWithNodeName(l.cs, l.ns.Name,
			[]*v1.PersistentVolumeClaim{l.resource1.pvc},
			false, "", false, false, framework.SELinuxLabel,
			nil, nodeName, framework.PodStartTimeout)
		defer func() {
			framework.ExpectNoError(framework.DeletePodWithWait(f, l.cs, pod1))
		}()
		Expect(err).NotTo(HaveOccurred())

		By("Creating 2nd pod with a different volume")
		pod2, err := framework.CreateSecPodWithNodeName(l.cs, l.ns.Name,
			[]*v1.PersistentVolumeClaim{l.resource2.pvc},
			false, "", false, false, framework.SELinuxLabel,
			nil, nodeName, framework.PodStartTimeout)
		defer func() {
			framework.ExpectNoError(framework.DeletePodWithWait(f, l.cs, pod2))
		}()
		Expect(err).NotTo(HaveOccurred())

		By("Checking if the volume in 1st pod exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod1, pattern.VolMode, "/mnt/volume1")

		By("Checking if read/write to the volume in 1st pod works properly")
		utils.CheckReadWriteToPath(pod1, pattern.VolMode, "/mnt/volume1")

		By("Checking if the volume in 2nd pod exists as expected volume mode")
		utils.CheckVolumeModeOfPath(pod2, pattern.VolMode, "/mnt/volume1")

		By("Checking if read/write to the volume in 2nd pod works properly")
		utils.CheckReadWriteToPath(pod2, pattern.VolMode, "/mnt/volume1")

		// TODO: then 1=fs,2=block is different from 1=block,2=fs
		By("Deleting the 1st pod")
		err = framework.DeletePodWithWait(f, f.ClientSet, pod1)
		Expect(err).ToNot(HaveOccurred(), "while deleting 1st pod %v", pod1)

		By("Checking if read/write to the volume in 2nd pod works properly")
		utils.CheckReadWriteToPath(pod2, pattern.VolMode, "/mnt/volume1")
	}

	if driver.GetDriverInfo().Name == "iscsi" {
		// This tests below configuration:
		// [pod1] [pod2]
		// [   node1   ]
		//   \      /     <- same volume mode
		//   [volume1]
		It("should attach iscsi volumes with same portal & iqn but different lun with the same volume mode to different pods on the same node and not logout when one pod is deleted", func() {
			init()
			defer cleanup()

			l.resource2.pattern.VolMode = l.resource1.pattern.VolMode
			testAttachOneVolumeToTwoPods(l)
		})
	}
}

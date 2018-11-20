/*
Copyright 2018 The Kubernetes Authors.

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

package resourcelock

import (
	"errors"
	"fmt"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"
)

type LeaseLock struct {
	// LeaseMeta should contain a Name and a Namespace of a
	// LeaseMeta object that the LeaderElector will attempt to lead.
	LeaseMeta  metav1.ObjectMeta
	Client     coordinationv1client.LeasesGetter
	LockConfig ResourceLockConfig
	lease      *coordinationv1.Lease
}

// Get returns the election record from a Lease spec
func (ll *LeaseLock) Get() (*LeaderElectionRecord, error) {
	var err error
	ll.lease, err = ll.Client.Leases(ll.LeaseMeta.Namespace).Get(ll.LeaseMeta.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	holderIdentity := ""
	if ll.lease.Spec.HolderIdentity != nil {
		holderIdentity = *ll.lease.Spec.HolderIdentity
	}
	leaseDurationSeconds := 0
	if ll.lease.Spec.LeaseDurationSeconds != nil {
		leaseDurationSeconds = int(*ll.lease.Spec.LeaseDurationSeconds)
	}
	leaseTransitions := 0
	if ll.lease.Spec.LeaseTransitions != nil {
		leaseTransitions = int(*ll.lease.Spec.LeaseTransitions)
	}
	return &LeaderElectionRecord{
		HolderIdentity:       holderIdentity,
		LeaseDurationSeconds: leaseDurationSeconds,
		AcquireTime:          metav1.Time{ll.lease.Spec.AcquireTime.Time},
		RenewTime:            metav1.Time{ll.lease.Spec.RenewTime.Time},
		LeaderTransitions:    leaseTransitions,
	}, nil
}

// Create attempts to create a Lease
func (ll *LeaseLock) Create(ler LeaderElectionRecord) error {
	var err error
	ll.lease, err = ll.Client.Leases(ll.LeaseMeta.Namespace).Create(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ll.LeaseMeta.Name,
			Namespace: ll.LeaseMeta.Namespace,
		},
		Spec: LeaderElectionRecordToLeaseSpec(&ler),
	})
	return err
}

// Update will update an existing Lease spec.
func (ll *LeaseLock) Update(ler LeaderElectionRecord) error {
	if ll.lease == nil {
		return errors.New("lease not initialized, call get or create first")
	}
	ll.lease.Spec = LeaderElectionRecordToLeaseSpec(&ler)
	var err error
	ll.lease, err = ll.Client.Leases(ll.LeaseMeta.Namespace).Update(ll.lease)
	return err
}

// RecordEvent in leader election while adding meta-data
func (ll *LeaseLock) RecordEvent(s string) {
	events := fmt.Sprintf("%v %v", ll.LockConfig.Identity, s)
	ll.LockConfig.EventRecorder.Eventf(&coordinationv1.Lease{ObjectMeta: ll.lease.ObjectMeta}, corev1.EventTypeNormal, "LeaderElection", events)
}

// Describe is used to convert details on current resource lock
// into a string
func (ll *LeaseLock) Describe() string {
	return fmt.Sprintf("%v/%v", ll.LeaseMeta.Namespace, ll.LeaseMeta.Name)
}

// returns the Identity of the lock
func (ll *LeaseLock) Identity() string {
	return ll.LockConfig.Identity
}

func LeaderElectionRecordToLeaseSpec(ler *LeaderElectionRecord) coordinationv1.LeaseSpec {
	leaseDurationSeconds := int32(ler.LeaseDurationSeconds)
	leaseTransitions := int32(ler.LeaderTransitions)
	return coordinationv1.LeaseSpec{
		HolderIdentity:       &ler.HolderIdentity,
		LeaseDurationSeconds: &leaseDurationSeconds,
		AcquireTime:          &metav1.MicroTime{ler.AcquireTime.Time},
		RenewTime:            &metav1.MicroTime{ler.RenewTime.Time},
		LeaseTransitions:     &leaseTransitions,
	}
}

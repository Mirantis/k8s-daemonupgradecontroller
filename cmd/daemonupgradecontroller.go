// Copyright 2016 Mirantis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	"k8s.io/kubernetes/cmd/kube-controller-manager/app/options"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	_ "k8s.io/kubernetes/pkg/client/metrics/prometheus" // for client metric registration
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/informers"
	"k8s.io/kubernetes/pkg/healthz"
	"k8s.io/kubernetes/pkg/util/flag"
	"k8s.io/kubernetes/pkg/util/logs"
	"k8s.io/kubernetes/pkg/util/wait"
	_ "k8s.io/kubernetes/pkg/util/workqueue/prometheus" // for workqueue metric registration
	_ "k8s.io/kubernetes/pkg/version/prometheus"        // for version metric registration
	"k8s.io/kubernetes/pkg/version/verflag"

	"github.com/spf13/pflag"

	daemonupgrade "github.com/Mirantis/k8s-daemonupgradecontroller/pkg/controller/daemon"
)

func ResyncPeriod(s *options.CMServer) func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(s.MinResyncPeriod.Nanoseconds()) * factor)
	}
}

func init() {
	healthz.DefaultHealthz()
}

func RunDaemonUpgrade(s *options.CMServer) error {
	kubeconfig, err := clientcmd.BuildConfigFromFlags(s.Master, s.Kubeconfig)
	if err != nil {
		return err
	}
	rootClientBuilder := controller.SimpleControllerClientBuilder{
		ClientConfig: kubeconfig,
	}
	client := func(serviceAccountName string) clientset.Interface {
		return rootClientBuilder.ClientOrDie(serviceAccountName)
	}
	sharedInformers := informers.NewSharedInformerFactory(client("shared-informers"), ResyncPeriod(s)())

	dsc := daemonupgrade.NewDaemonUpgradeController(sharedInformers.DaemonSets(), sharedInformers.Pods(), sharedInformers.Nodes(), client("daemon-set-controller"), int(s.LookupCacheSizeForDaemonSet))
	sharedInformers.Start(nil)
	dsc.Run(int(s.ConcurrentDaemonSetSyncs), wait.NeverStop)
	return nil
}

func main() {
	s := options.NewCMServer()
	s.AddFlags(pflag.CommandLine)

	flag.InitFlags()
	logs.InitLogs()
	defer logs.FlushLogs()

	verflag.PrintAndExitIfRequested()

	if err := RunDaemonUpgrade(s); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

}

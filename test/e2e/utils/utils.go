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

package utils

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/api/v1"
	clientset "k8s.io/kubernetes/pkg/client/clientset_generated/internalclientset"
	restclient "k8s.io/kubernetes/pkg/client/restclient"
	"k8s.io/kubernetes/pkg/client/unversioned/clientcmd"
	"k8s.io/kubernetes/pkg/client/unversioned/remotecommand"
	remotecommandserver "k8s.io/kubernetes/pkg/kubelet/server/remotecommand"
	"k8s.io/kubernetes/pkg/util/httpstream/spdy"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var MASTER string
var TESTLINK string

func init() {
	flag.StringVar(&MASTER, "master", "http://apiserver:8888", "apiserver address to use with restclient")
	flag.StringVar(&TESTLINK, "testlink", "eth0", "link to use on the side of tests")
}

func GetTestLink() string {
	return TESTLINK
}

func Logf(format string, a ...interface{}) {
	fmt.Fprintf(GinkgoWriter, fmt.Sprintf("%s\n", format), a...)
}

func LoadConfig() *restclient.Config {
	config, err := clientcmd.BuildConfigFromFlags(MASTER, "")
	Expect(err).NotTo(HaveOccurred())
	return config
}

func KubeClient() (*clientset.Clientset, error) {
	Logf("Using master %v\n", MASTER)
	config := LoadConfig()
	c, err := clientset.NewForConfig(config)
	Expect(err).NotTo(HaveOccurred())
	return c, nil
}

func WaitForReady(c *clientset.Clientset, pod *v1.Pod) {
	Eventually(func() error {
		podUpdated, err := c.Core().Pods(pod.Namespace).Get(pod.Name)
		if err != nil {
			return err
		}
		if podUpdated.Status.Phase != api.PodRunning {
			return fmt.Errorf("pod %v is not running phase: %v", podUpdated.Name, podUpdated.Status.Phase)
		}
		return nil
	}, 120*time.Second, 5*time.Second).Should(BeNil())
}

func DumpLogs(c *clientset.Clientset, pods ...v1.Pod) {
	for _, pod := range pods {
		dumpLogs(c, pod)
	}
}

func dumpLogs(c *clientset.Clientset, pod v1.Pod) {
	req := c.Core().Pods(pod.Namespace).GetLogs(pod.Name, &api.PodLogOptions{})
	readCloser, err := req.Stream()
	Expect(err).NotTo(HaveOccurred())
	defer readCloser.Close()
	Logf("\n Dumping logs for %v:%v \n", pod.Namespace, pod.Name)
	_, err = io.Copy(GinkgoWriter, readCloser)
	Expect(err).NotTo(HaveOccurred())
}

func ExecInPod(c *clientset.Clientset, pod v1.Pod, cmd ...string) string {
	Logf("Running %v in %v\n", cmd, pod.Name)

	container := pod.Spec.Containers[0].Name
	var stdout, stderr bytes.Buffer
	config := LoadConfig()
	rest := c.Core().RESTClient()
	req := rest.Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		Param("container", container)
	req.VersionedParams(&api.PodExecOptions{
		Container: container,
		Command:   cmd,
		TTY:       false,
		Stdin:     false,
		Stdout:    true,
		Stderr:    true,
	}, api.ParameterCodec)
	err := execute("POST", req.URL(), config, nil, &stdout, &stderr, false)
	Logf("Error %v: %v\n", cmd, stderr.String())
	Expect(err).NotTo(HaveOccurred())
	Logf("Output %v: %v\n", cmd, stdout.String())
	return strings.TrimSpace(stdout.String())
}

func execute(method string, url *url.URL, config *restclient.Config, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
	tlsConfig, err := restclient.TLSConfigFor(config)
	if err != nil {
		return err
	}
	upgrader := spdy.NewRoundTripper(tlsConfig)
	exec, err := remotecommand.NewStreamExecutor(upgrader, nil, method, url)
	if err != nil {
		return err
	}
	return exec.Stream(remotecommand.StreamOptions{
		SupportedProtocols: remotecommandserver.SupportedStreamingProtocols,
		Stdin:              stdin,
		Stdout:             stdout,
		Stderr:             stderr,
		Tty:                tty,
	})
}

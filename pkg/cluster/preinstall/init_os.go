/*
Copyright 2020 The KubeSphere Authors.

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

package preinstall

import (
	"encoding/base64"
	"fmt"
	kubekeyapi "github.com/kubesphere/kubekey/pkg/apis/kubekey/v1alpha1"
	"github.com/kubesphere/kubekey/pkg/cluster/preinstall/tmpl"
	"github.com/kubesphere/kubekey/pkg/util/manager"
	"github.com/pkg/errors"
)

const (
	binDir                       = "/usr/local/bin"
	kubeConfigDir                = "/etc/kubernetes"
	kubeCertDir                  = "/etc/kubernetes/pki"
	kubeManifestDir              = "/etc/kubernetes/manifests"
	kubeScriptDir                = "/usr/local/bin/kube-scripts"
	kubeletFlexvolumesPluginsDir = "/usr/libexec/kubernetes/kubelet-plugins/volume/exec"
)

func DownloadBinaries(mgr *manager.Manager) error {
	if err := Prepare(mgr); err != nil {
		return errors.Wrap(err, "Failed to load kube binaries")
	}
	return nil
}

func InitOS(mgr *manager.Manager) error {

	mgr.Logger.Infoln("Configurating operating system ...")

	// initOsOnNode 方法执行初始化操作
	return mgr.RunTaskOnAllNodes(initOsOnNode, true)
}

func initOsOnNode(mgr *manager.Manager, node *kubekeyapi.HostCfg) error {

	// 在节点上创建一个名字为kube的用户
	_ = addUsers(mgr, node)

	// 创建一些必要的目录
	if err := createDirectories(mgr, node); err != nil {
		return err
	}

	// 判断 /tmp/kubekey 是否存在，如果已经存在，则删除它
	// 删除成功之后，重新创建一个 /tmp/kubekey 目录
	tmpDir := "/tmp/kubekey"
	_, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"if [ -d %s ]; then rm -rf %s ;fi\" && mkdir -p %s", tmpDir, tmpDir, tmpDir), 1, false)
	if err != nil {
		return errors.Wrap(errors.WithStack(err), "Failed to create tmp dir")
	}

	// 从node中获取name，设置当前节点的hostname
	// 把/etc/hosts文件中 127.0.0.1 开头的开头也替换为nodename //todo 实际我查看似乎没有替换
	_, err1 := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"hostnamectl set-hostname %s && sed -i '/^127.0.1.1/s/.*/127.0.1.1      %s/g' /etc/hosts\"", node.Name, node.Name), 1, false)
	if err1 != nil {
		return errors.Wrap(errors.WithStack(err1), "Failed to override hostname")
	}

	// 初始化脚本
	// 这个脚本用于执行一系列初始化操作系统的任务，包括关闭交换分区、设置内核参数、停用防火墙、加载内核模块等。这些任务通常是为了在Kubernetes集群部署过程中准备操作系统环境而执行的。
	initOsScript, err2 := tmpl.InitOsScript(mgr)
	if err2 != nil {
		return err2
	}

	// 把脚本写入文件initOS.sh中
	str := base64.StdEncoding.EncodeToString([]byte(initOsScript))
	_, err3 := mgr.Runner.ExecuteCmd(fmt.Sprintf("echo %s | base64 -d > %s/initOS.sh && chmod +x %s/initOS.sh", str, tmpDir, tmpDir), 1, false)
	if err3 != nil {
		return errors.Wrap(errors.WithStack(err3), "Failed to generate init os script")
	}

	// 把initOS.sh脚本拷贝到 /usr/local/bin/kube-scripts 目录中执行
	_, err4 := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo cp %s/initOS.sh %s && sudo %s/initOS.sh", tmpDir, kubeScriptDir, kubeScriptDir), 1, true)
	if err4 != nil {
		return errors.Wrap(errors.WithStack(err4), "Failed to configure operating system")
	}
	return nil
}

func addUsers(mgr *manager.Manager, node *kubekeyapi.HostCfg) error {
	if _, err := mgr.Runner.ExecuteCmd("sudo -E /bin/sh -c \"useradd -M -c 'Kubernetes user' -s /sbin/nologin -r kube || :\"", 1, false); err != nil {
		return err
	}

	if node.IsEtcd {
		if _, err := mgr.Runner.ExecuteCmd("sudo -E /bin/sh -c \"useradd -M -c 'Etcd user' -s /sbin/nologin -r etcd || :\"", 1, false); err != nil {
			return err
		}
	}

	return nil
}

func createDirectories(mgr *manager.Manager, node *kubekeyapi.HostCfg) error {
	dirs := []string{binDir, kubeConfigDir, kubeCertDir, kubeManifestDir, kubeScriptDir, kubeletFlexvolumesPluginsDir}
	// 遍历上面所有的目录，然后创建这些目录
	for _, dir := range dirs {
		if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"mkdir -p %s\"", dir), 1, false); err != nil {
			return err
		}

		// 如果是kubeletFlexvolumesPluginsDir
		// 把"/usr/libexec/kubernetes目录，更改所有者为kube用户
		if dir == kubeletFlexvolumesPluginsDir {
			if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"chown kube -R %s\"", "/usr/libexec/kubernetes"), 1, false); err != nil {
				return err
			}
		} else {
			// 把剩下的路径的所有者都更改为kube用户
			if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"chown kube -R %s\"", dir), 1, false); err != nil {
				return err
			}
		}
	}

	// 创建/etc/cni/net.d 目录，把/etc/cni的所有者更改为kube用户
	if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"mkdir -p %s && chown kube -R %s\"", "/etc/cni/net.d", "/etc/cni"), 1, false); err != nil {
		return err
	}

	// 创建 /opt/cni/bin 目录，把/opt/cni的所有者更改为kube用户
	if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"mkdir -p %s && chown kube -R %s\"", "/opt/cni/bin", "/opt/cni"), 1, false); err != nil {
		return err
	}

	// 创建 /var/lib/calico 目录，把/var/lib/calico的所有者更改为kube用户
	if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"mkdir -p %s && chown kube -R %s\"", "/var/lib/calico", "/var/lib/calico"), 1, false); err != nil {
		return err
	}

	// 如果是etcd节点
	// 创建/var/lib/etcd，把/var/lib/etcd的所有者更改为etcd用户
	if node.IsEtcd {
		if _, err := mgr.Runner.ExecuteCmd(fmt.Sprintf("sudo -E /bin/sh -c \"mkdir -p %s && chown etcd -R %s\"", "/var/lib/etcd", "/var/lib/etcd"), 1, false); err != nil {
			return err
		}
	}

	return nil
}

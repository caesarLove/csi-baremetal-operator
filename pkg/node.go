package pkg

import (
	"strconv"

	"github.com/go-logr/logr"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"

	csibaremetalv1 "github.com/dell/csi-baremetal-operator/api/v1"
	"github.com/dell/csi-baremetal-operator/api/v1/components"
)

const (
	nodeName                  = CSIName + "-node"
	nodeServiceAccountName    = "csi-node-sa"
	loopbackManagerConfigName = "loopback-config"

	// ports
	driveManagerPort = 8888

	// volumes
	registrationDirVolume = "registration-dir"
	hostDevVolume         = "host-dev"
	hostHomeVolume        = "host-home"
	hostSysVolume         = "host-sys"
	hostRootVolume        = "host-root"
	hostRunUdevVolume     = "host-run-udev"
	hostRunLVMVolume      = "host-run-lvm"
	hostRunLock           = "host-run-lock"
	mountPointDirVolume   = "mountpoint-dir"
	csiPathVolume         = "csi-path"
	driveConfigVolume     = "drive-config"

	livenessProbeSidecar   = "liveness-probe"
	driverRegistrarSidecar = "csi-node-driver-registrar"
)

type Node struct {
	kubernetes.Clientset
	logr.Logger
}

func (n *Node) Update(csi *csibaremetalv1.Deployment) error {
	namespace := GetNamespace(csi)
	dsClient := n.AppsV1().DaemonSets(namespace)

	isDeployed, err := isDaemonSetDeployed(dsClient, nodeName)
	if err != nil {
		n.Logger.Error(err, "Failed to get daemon set")
		return err
	}

	if isDeployed {
		n.Logger.Info("Daemon set already deployed")
		return nil
	}

	// create daemonset
	ds := createNodeDaemonSet(csi)
	if _, err := dsClient.Create(ds); err != nil {
		n.Logger.Error(err, "Failed to create daemon set")
		return err
	}

	n.Logger.Info("Daemon set created successfully")
	return nil
}

func createNodeDaemonSet(csi *csibaremetalv1.Deployment) *v1.DaemonSet {
	namespace := GetNamespace(csi)
	return &v1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: nodeName, Namespace: namespace},
		Spec: v1.DaemonSetSpec{
			// selector
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": nodeName},
			},
			// template
			Template: corev1.PodTemplateSpec{
				// labels and annotations
				ObjectMeta: metav1.ObjectMeta{
					// labels
					Labels: map[string]string{
						"app":                    nodeName,
						"app.kubernetes.io/name": CSIName,
					},
					// integration with monitoring
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   strconv.Itoa(PrometheusPort),
						"prometheus.io/path":   "/metrics",
					},
				},
				Spec: corev1.PodSpec{
					Volumes:                       createNodeVolumes(),
					Containers:                    createNodeContainers(csi),
					TerminationGracePeriodSeconds: pointer.Int64Ptr(TerminationGracePeriodSeconds),
					NodeSelector:                  map[string]string{},
					ServiceAccountName:            nodeServiceAccountName,
					HostIPC:                       true,
				},
			},
		},
	}
}

func createNodeVolumes() []corev1.Volume {
	directory := corev1.HostPathDirectory
	directoryOrCreate := corev1.HostPathDirectoryOrCreate

	return []corev1.Volume{
		{Name: LogsVolume, VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		}},
		{Name: hostDevVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/dev", Type: &directory},
		}},
		{Name: hostHomeVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/home", Type: &directory},
		}},
		{Name: hostSysVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/sys", Type: &directory},
		}},
		{Name: hostRootVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/", Type: &directory},
		}},
		{Name: hostRunUdevVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/run/udev", Type: &directory},
		}},
		{Name: hostRunLVMVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/run/lvm", Type: &directory},
		}},
		{Name: hostRunLock, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/run/lock", Type: &directory},
		}},
		{Name: CSISocketDirVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins/csi-baremetal", Type: &directoryOrCreate},
		}},
		{Name: registrationDirVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins_registry/", Type: &directoryOrCreate},
		}},
		{Name: mountPointDirVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/pods", Type: &directory},
		}},
		{Name: csiPathVolume, VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/kubelet/plugins/kubernetes.io/csi"},
		}},
		{Name: driveConfigVolume, VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: loopbackManagerConfigName},
				Optional:             pointer.BoolPtr(true),
			},
		}},
	}
}

// todo split long methods - https://github.com/dell/csi-baremetal/issues/329
func createNodeContainers(csi *csibaremetalv1.Deployment) []corev1.Container {
	var (
		lp            *components.Sidecar
		dr            *components.Sidecar
		bidirectional = corev1.MountPropagationBidirectional
		driveMgr      = csi.Spec.Driver.Node.DriveMgr
		node          = csi.Spec.Driver.Node
		testEnv       = csi.Spec.Driver.Node.TestEnv
	)
	for _, v := range csi.Spec.Driver.Node.Sidecars {
		if v.Name == livenessProbeSidecar {
			lp = v
			continue
		}
		if v.Name == driverRegistrarSidecar {
			dr = v
			continue
		}
	}
	if lp == nil {
		lp = constructSidecar(livenessProbeSidecar, csi.Spec.GlobalRegistry, "v2.1.0", "Always")
	}
	if dr == nil {
		dr = constructSidecar(driverRegistrarSidecar, csi.Spec.GlobalRegistry, "v1.0.1-gke.0", "Always")
	}
	return []corev1.Container{
		{
			Name:            "liveness-probe",
			Image:           constructImage(testEnv, lp.Image),
			ImagePullPolicy: corev1.PullPolicy(lp.Image.PullPolicy),
			Args:            []string{"--csi-address=/csi/csi.sock"},
			VolumeMounts: []corev1.VolumeMount{
				{Name: CSISocketDirVolume, MountPath: "/csi"},
			},
		},
		{
			Name:            "csi-node-driver-registrar",
			Image:           constructImage(testEnv, dr.Image),
			ImagePullPolicy: corev1.PullPolicy(dr.Image.PullPolicy),
			Args: []string{"--v=5", "--csi-address=$(ADDRESS)",
				"--kubelet-registration-path=$(DRIVER_REG_SOCK_PATH)"},
			Lifecycle: &corev1.Lifecycle{PreStop: &corev1.Handler{Exec: &corev1.ExecAction{Command: []string{
				"/bin/sh", "-c", "rm -rf /registration/csi-baremetal /registration/csi-baremetal-reg.sock"}}}},
			Env: []corev1.EnvVar{
				{Name: "ADDRESS", Value: "/csi/csi.sock"},
				{Name: "DRIVER_REG_SOCK_PATH", Value: "/var/lib/kubelet/plugins/csi-baremetal/csi.sock"},
				{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "spec.nodeName"},
				}},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: CSISocketDirVolume, MountPath: "/csi"},
				{Name: registrationDirVolume, MountPath: "/registration"},
			},
		},
		{
			Name:            "node",
			Image:           constructImage(testEnv, node.Image),
			ImagePullPolicy: corev1.PullPolicy(node.Image.PullPolicy),
			Args: []string{
				"--csiendpoint=$(CSI_ENDPOINT)",
				"--nodename=$(KUBE_NODE_NAME)",
				"--namespace=$(NAMESPACE)",
				"--extender=true",
				"--usenodeannotation=" + strconv.FormatBool(UseNodeAnnotation),
				"--loglevel=info",
				"--metrics-address=:" + strconv.Itoa(PrometheusPort),
				"--metrics-path=/metrics",
				"--drivemgrendpoint=tcp://localhost:" + strconv.Itoa(driveManagerPort),
			},
			Ports: []corev1.ContainerPort{
				{Name: LivenessPort, ContainerPort: 9808, Protocol: corev1.ProtocolTCP},
				{Name: "metrics", ContainerPort: PrometheusPort, Protocol: corev1.ProtocolTCP},
			},
			LivenessProbe: &corev1.Probe{
				Handler: corev1.Handler{HTTPGet: &corev1.HTTPGetAction{
					Path: "/healthz",
					Port: intstr.FromString(LivenessPort)}},
				InitialDelaySeconds: 300,
				TimeoutSeconds:      3,
				PeriodSeconds:       10,
				FailureThreshold:    5,
			},
			ReadinessProbe: &corev1.Probe{
				Handler: corev1.Handler{Exec: &corev1.ExecAction{Command: []string{
					"/health_probe",
					"-addr=:9999"}}},
				InitialDelaySeconds: 3,
				PeriodSeconds:       3,
				SuccessThreshold:    3,
				FailureThreshold:    100,
			},
			Env: []corev1.EnvVar{
				{Name: "CSI_ENDPOINT", Value: "unix:///csi/csi.sock"},
				{Name: "LOG_FORMAT", Value: "text"},
				{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "spec.nodeName"},
				}},
				{Name: "MY_POD_IP", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
				}},
				{Name: "NAMESPACE", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "metadata.namespace"},
				}},
			},
			SecurityContext: &corev1.SecurityContext{Privileged: pointer.BoolPtr(true)},
			VolumeMounts: []corev1.VolumeMount{
				{Name: LogsVolume, MountPath: "/var/log"},
				{Name: hostDevVolume, MountPath: "/dev"},
				{Name: hostSysVolume, MountPath: "/sys"},
				{Name: hostRunUdevVolume, MountPath: "/run/udev"},
				{Name: hostRunLVMVolume, MountPath: "/run/lvm"},
				{Name: hostRunLock, MountPath: "/run/lock"},
				{Name: CSISocketDirVolume, MountPath: "/csi"},
				{Name: mountPointDirVolume, MountPath: "/var/lib/kubelet/pods", MountPropagation: &bidirectional},
				{Name: csiPathVolume, MountPath: "/var/lib/kubelet/plugins/kubernetes.io/csi", MountPropagation: &bidirectional},
				{Name: hostRootVolume, MountPath: "/hostroot", MountPropagation: &bidirectional},
			},
		},
		{
			Name:            "drivemgr",
			Image:           constructImage(testEnv, driveMgr.Image),
			ImagePullPolicy: corev1.PullPolicy(driveMgr.Image.PullPolicy),
			Args: []string{
				"--loglevel=info",
				"--drivemgrendpoint=tcp://localhost:" + strconv.Itoa(driveManagerPort),
				"--usenodeannotation=" + strconv.FormatBool(UseNodeAnnotation),
			},
			Env: []corev1.EnvVar{
				{Name: "LOG_FORMAT", Value: "text"},
				{Name: "KUBE_NODE_NAME", ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{APIVersion: "v1", FieldPath: "spec.nodeName"},
				}},
			},
			SecurityContext: &corev1.SecurityContext{Privileged: pointer.BoolPtr(true)},
			VolumeMounts: []corev1.VolumeMount{
				{Name: hostDevVolume, MountPath: "/dev"},
				{Name: hostHomeVolume, MountPath: "/host/home"},
				{Name: driveConfigVolume, MountPath: "/etc/config"},
			},
		},
	}
}

func constructImage(isTest bool, image *components.Image) string {
	if isTest {
		return image.Name + ":" + image.Tag
	}
	return image.Registry + "/" + image.Name + ":" + image.Tag
}

func constructSidecar(name, registry, tag, pullPolicy string) *components.Sidecar {
	return &components.Sidecar{
		Name: name,
		Image: &components.Image{
			Name:       name,
			Registry:   registry,
			Tag:        tag,
			PullPolicy: pullPolicy,
		},
	}
}

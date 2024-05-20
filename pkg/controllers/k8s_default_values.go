package controllers

import corev1 "k8s.io/api/core/v1"

func fillContainerDefaultValues(c *corev1.Container) {
	for i := range c.Ports {
		if c.Ports[i].Protocol == "" {
			c.Ports[i].Protocol = corev1.ProtocolTCP
		}
	}
	if c.ImagePullPolicy == "" {
		c.ImagePullPolicy = corev1.PullIfNotPresent
	}
	if c.TerminationMessagePath == "" {
		c.TerminationMessagePath = "/dev/termination-log"
	}
	if c.TerminationMessagePolicy == "" {
		c.TerminationMessagePolicy = corev1.TerminationMessageReadFile
	}
}

func fillConfigMapVolumeDefaultValues(v *corev1.Volume) {
	if v.ConfigMap == nil {
		return
	}
	if v.ConfigMap.DefaultMode == nil {
		v.ConfigMap.DefaultMode = int32Ptr(int(DefaultConfigMapMode))
	}
}

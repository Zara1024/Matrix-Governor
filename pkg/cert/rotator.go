package cert

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// CertPair 保存 PEM 编码的证书与私钥
type CertPair struct {
	CertPEM []byte
	KeyPEM  []byte
	CAPEM   []byte
}

// GenerateSelfSignedCert 纯 Go 生成具备完整 SAN 扩展的自签名 CA 与服务端 TLS 证书
func GenerateSelfSignedCert(serviceName, namespace string) (*CertPair, error) {
	// 1. 生成 CA 私钥与自签 CA 证书 (10年有效期)
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 私钥失败: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName:   "matrix-governor-ca",
			Organization: []string{"matrix-governor.io"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("生成 CA 证书失败: %v", err)
	}

	// 2. 生成服务端私钥与证书
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("生成服务端私钥失败: %v", err)
	}

	dnsNames := []string{
		serviceName,
		fmt.Sprintf("%s.%s", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
		"localhost",
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano() + 1),
		Subject: pkix.Name{
			CommonName:   fmt.Sprintf("%s.%s.svc", serviceName, namespace),
			Organization: []string{"matrix-governor.io"},
		},
		DNSNames:              dnsNames,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(5 * 365 * 24 * time.Hour), // 服务端证书 5 年有效
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("签署服务端证书失败: %v", err)
	}

	// 3. 编码为 PEM 格式
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})

	return &CertPair{
		CertPEM: serverCertPEM,
		KeyPEM:  serverKeyPEM,
		CAPEM:   caPEM,
	}, nil
}

// AutoPatchWebhookCABundle 自动将自签 CA Bundle 刷入 K8s MutatingWebhookConfiguration
func AutoPatchWebhookCABundle(ctx context.Context, kubeClient kubernetes.Interface, webhookConfigName string, caBundle []byte) error {
	if kubeClient == nil {
		return nil
	}

	webhookConfig, err := kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Get(ctx, webhookConfigName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("获取 MutatingWebhookConfiguration %s 失败: %v", webhookConfigName, err)
	}

	updated := false
	for i := range webhookConfig.Webhooks {
		if !bytes.Equal(webhookConfig.Webhooks[i].ClientConfig.CABundle, caBundle) {
			webhookConfig.Webhooks[i].ClientConfig.CABundle = caBundle
			updated = true
		}
	}

	if updated {
		_, err = kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Update(ctx, webhookConfig, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("更新 Webhook CA Bundle 失败: %v", err)
		}
		klog.Infof("[CertRotator] 成功自动刷新 MutatingWebhookConfiguration %s 的 CA Bundle (零运维自愈)", webhookConfigName)
	}

	return nil
}

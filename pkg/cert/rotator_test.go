package cert

import (
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	pair, err := GenerateSelfSignedCert("matrix-governor-svc", "matrix-system")
	if err != nil {
		t.Fatalf("生成自签证书失败: %v", err)
	}

	if len(pair.CertPEM) == 0 || len(pair.KeyPEM) == 0 || len(pair.CAPEM) == 0 {
		t.Fatalf("生成的证书或私钥内容为空")
	}

	// 验证证书内容与 SAN 扩展
	block, _ := pem.Decode(pair.CertPEM)
	if block == nil {
		t.Fatalf("解析 Server 证书 PEM 失败")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("反序列化 x509 证书失败: %v", err)
	}

	expectedDNS := "matrix-governor-svc.matrix-system.svc"
	found := false
	for _, dns := range cert.DNSNames {
		if dns == expectedDNS {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("证书 SAN 域名中未找到预期的 Service 域名: %s", expectedDNS)
	}
}

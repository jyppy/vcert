/*
 * Copyright 2023 Venafi, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *  http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package installer

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"go.uber.org/zap"
	"software.sslmate.com/src/go-pkcs12"

	"github.com/Venafi/vcert/v5/pkg/certificate"
	"github.com/Venafi/vcert/v5/pkg/playbook/app/domain"
	"github.com/Venafi/vcert/v5/pkg/playbook/util"
)

// PKCS12Installer represents an installation that will use the PKCS12 format for the certificate bundle
type PKCS12Installer struct {
	domain.Installation
}

// NewPKCS12Installer returns a new installer of type PKCS12 with the values defined in inst
func NewPKCS12Installer(inst domain.Installation) PKCS12Installer {
	return PKCS12Installer{inst}
}

// Check is the method in charge of making the validations to install a new certificate:
// 1. Does the certificate exists? > Install if it doesn't.
// 2. Does the certificate is about to expire? Renew if about to expire.
// Returns true if the certificate needs to be installed.
func (r PKCS12Installer) Check(renewBefore string, _ domain.PlaybookRequest) (bool, error) {
	zap.L().Info("checking certificate health", zap.String("format", r.Type.String()), zap.String("location", r.File))

	// Check certificate file exists
	certExists, err := util.FileExists(r.File)
	if err != nil {
		return false, err
	}
	if !certExists {
		return true, nil
	}

	// Load Certificate
	cert, err := loadPKCS12(r.File, r.P12Password)
	if err != nil {
		return false, err
	}

	// Check certificate expiration
	renew := needRenewal(cert, renewBefore)

	return renew, nil
}

// Backup takes the certificate request and backs up the current version prior to overwriting
func (r PKCS12Installer) Backup() error {
	zap.L().Debug("backing up certificate", zap.String("location", r.File))

	// Check certificate file exists
	certExists, err := util.FileExists(r.File)
	if err != nil {
		return err
	}
	if !certExists {
		zap.L().Info("new certificate location specified, no back up taken")
		return nil
	}

	newLocation := fmt.Sprintf("%s.bak", r.File)

	err = util.CopyFile(r.File, newLocation)
	if err != nil {
		return err
	}

	zap.L().Info("certificate backed up", zap.String("location", r.File), zap.String("backupLocation", newLocation))
	return err
}

// Install takes the certificate bundle and moves it to the location specified in the installer
func (r PKCS12Installer) Install(pcc certificate.PEMCollection) error {
	zap.L().Debug("installing certificate", zap.String("location", r.File))

	if r.P12Password == "" {
		return domain.ErrNoP12Password
	}

	content, err := packageAsPKCS12(pcc, r.P12Password)
	if err != nil {
		zap.L().Error("could not package certificate as PKCS12")
		return err
	}

	err = util.WriteFile(r.File, content)
	if err != nil {
		return err
	}

	return nil
}

// AfterInstallActions runs any instructions declared in the Installer on a terminal.
//
// No validations happen over the content of the AfterAction string, so caution is advised
func (r PKCS12Installer) AfterInstallActions() (string, error) {
	zap.L().Debug("running after-install actions", zap.String("location", r.File))

	result, err := util.ExecuteScript(r.AfterAction)
	return result, err
}

// InstallValidationActions runs any instructions declared in the Installer on a terminal and expects
// "0" for successful validation and "1" for a validation failure
// No validations happen over the content of the InstallValidation string, so caution is advised
func (r PKCS12Installer) InstallValidationActions() (string, error) {
	zap.L().Debug("running install validation actions", zap.String("location", r.File))

	validationResult, err := util.ExecuteScript(r.InstallValidation)
	if err != nil {
		return "", err
	}

	return validationResult, err
}

func loadPKCS12(pkcs12File string, keyPassword string) (*x509.Certificate, error) {
	//Open file
	data, err := os.ReadFile(pkcs12File)
	if err != nil {
		zap.L().Error("could not read PKCS12 file", zap.String("location", pkcs12File))
		return nil, err
	}

	// Due to limitations in pkcs12
	_, cert, _, err := pkcs12.DecodeChain(data, keyPassword)
	if err != nil {
		return nil, err
	}

	return cert, nil
}

func packageAsPKCS12(pcc certificate.PEMCollection, keyPassword string) ([]byte, error) {
	if len(pcc.Certificate) == 0 || len(pcc.PrivateKey) == 0 {
		return nil, fmt.Errorf("certificate and Private Key are required for PKCS12")
	}

	//Getting the certificate in bytes
	certBlock, _ := pem.Decode([]byte(pcc.Certificate))
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("missing Certificate PEM")
	}

	//Getting X509.Certificate object
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Certificate bytes to X509.Certificate object")
	}

	//Getting Chain as X509.Certificate objects
	chainList, err := getX509CertChain(pcc.Chain)
	if err != nil {
		return nil, err
	}

	//Getting the Private Key
	privateKey, err := getPrivateKey(pcc.PrivateKey, keyPassword)
	if err != nil {
		return nil, err
	}

	bytes, err := pkcs12.Encode(rand.Reader, privateKey, cert, chainList, keyPassword)
	if err != nil {
		return nil, fmt.Errorf("PKCS12 encode error: %w", err)
	}

	return bytes, nil
}

func getX509CertChain(chain []string) ([]*x509.Certificate, error) {
	chainList := make([]*x509.Certificate, 0)
	for _, chainCertStr := range chain {
		chainBlock, _ := pem.Decode([]byte(chainCertStr))
		chainCert, err := x509.ParseCertificate(chainBlock.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse Chain Certificate bytes to X509.Certificate")
		}
		chainList = append(chainList, chainCert)
	}

	return chainList, nil
}

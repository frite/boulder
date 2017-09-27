package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"flag"
	"math/big"
	"os"

	"github.com/miekg/pkcs11"
)

func rsaArgs(label string, mod int) ([]*pkcs11.Mechanism, []*pkcs11.Attribute, []*pkcs11.Attribute) {
	return []*pkcs11.Mechanism{
			pkcs11.NewMechanism(pkcs11.CKM_RSA_PKCS_KEY_PAIR_GEN, nil),
		},
		[]*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
			pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
			pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
			pkcs11.NewAttribute(pkcs11.CKA_MODULUS_BITS, mod),
			pkcs11.NewAttribute(pkcs11.CKA_PUBLIC_EXPONENT, []byte{1, 0, 1}), // 65537
		}, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
			pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
			pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
			pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
			pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		}
}

func rsaPub(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, object pkcs11.ObjectHandle) *rsa.PublicKey {
	attrs, err := ctx.GetAttributeValue(session, object, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_PUBLIC_EXPONENT, nil),
		pkcs11.NewAttribute(pkcs11.CKA_MODULUS, nil),
	})
	if err != nil {
		panic(err)
	}

	pubKey := &rsa.PublicKey{}
	gotMod, gotExp := false, false
	for _, a := range attrs {
		switch a.Type {
		case pkcs11.CKA_PUBLIC_EXPONENT:
			pubKey.E = int(big.NewInt(0).SetBytes(a.Value).Int64())
			gotExp = true
		case pkcs11.CKA_MODULUS:
			pubKey.N = big.NewInt(0).SetBytes(a.Value)
			gotMod = true
		}
	}
	if !gotExp || !gotMod {
		panic("Couldn't retrieve modulus or exponent")
	}
	return pubKey
}

var stringToCurve = map[string]elliptic.Curve{
	"P224": elliptic.P224(),
	"P256": elliptic.P256(),
	"P384": elliptic.P384(),
	"P521": elliptic.P521(),
}

var curveToOID = map[elliptic.Curve]asn1.ObjectIdentifier{
	elliptic.P224(): asn1.ObjectIdentifier{1, 3, 132, 0, 33},
	elliptic.P256(): asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7},
	elliptic.P384(): asn1.ObjectIdentifier{1, 3, 132, 0, 34},
	elliptic.P521(): asn1.ObjectIdentifier{1, 3, 132, 0, 35},
}

func ecArgs(label string, curve elliptic.Curve) ([]*pkcs11.Mechanism, []*pkcs11.Attribute, []*pkcs11.Attribute) {
	encodedCurve, err := asn1.Marshal(curveToOID[curve])
	if err != nil {
		panic(err)
	}
	return []*pkcs11.Mechanism{
			pkcs11.NewMechanism(pkcs11.CKM_EC_KEY_PAIR_GEN, nil),
		}, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
			pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
			pkcs11.NewAttribute(pkcs11.CKA_VERIFY, true),
			pkcs11.NewAttribute(pkcs11.CKA_EC_PARAMS, encodedCurve),
		}, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_LABEL, label),
			pkcs11.NewAttribute(pkcs11.CKA_TOKEN, true),
			pkcs11.NewAttribute(pkcs11.CKA_SENSITIVE, true),
			pkcs11.NewAttribute(pkcs11.CKA_EXTRACTABLE, false),
			pkcs11.NewAttribute(pkcs11.CKA_SIGN, true),
		}
}

func ecPub(ctx *pkcs11.Ctx, session pkcs11.SessionHandle, object pkcs11.ObjectHandle, curve elliptic.Curve) *ecdsa.PublicKey {
	attrs, err := ctx.GetAttributeValue(session, object, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PUBLIC_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_KEY_TYPE, pkcs11.CKK_EC),
		pkcs11.NewAttribute(pkcs11.CKA_EC_POINT, nil),
	})
	if err != nil {
		panic(err)
	}

	pubKey := &ecdsa.PublicKey{Curve: curve}
	gotPoint := false
	for _, a := range attrs {
		switch a.Type {
		case pkcs11.CKA_EC_POINT:
			x, y := elliptic.Unmarshal(curve, a.Value)
			if x == nil {
				// http://docs.oasis-open.org/pkcs11/pkcs11-curr/v2.40/os/pkcs11-curr-v2.40-os.html#_ftn1
				// PKCS#11 v2.20 specified that the CKA_EC_POINT was to be store in a DER-encoded
				// OCTET STRING.
				var point asn1.RawValue
				_, err = asn1.Unmarshal(a.Value, &point)
				if err != nil {
					panic(err)
				}
				if len(point.Bytes) == 0 {
					panic("Invalid CKA_EC_POINT value")
				}
				x, y = elliptic.Unmarshal(curve, point.Bytes)
			}
			pubKey.X, pubKey.Y = x, y
			gotPoint = true
			break
		}
	}
	if !gotPoint {
		panic("Couldn't retrieve EC point")
	}
	return pubKey
}

func main() {
	module := flag.String("module", "", "PKCS#11 module to use")
	keyType := flag.String("type", "", "Type of key to generate (RSA or ECDSA)")
	slot := flag.Uint("slot", 0, "Slot to generate key in")
	pin := flag.String("pin", "", "PIN for slot")
	label := flag.String("label", "", "Key label")
	rsaModLen := flag.Int("modulus-bits", 0, "Size of RSA modulus in bits. Only valid if --type=RSA")
	ecdsaCurve := flag.String("curve", "", "Type of ECDSA curve to use (). Only valid if --type=ECDSA")
	flag.Parse()

	if *module == "" {
		panic("--module is required")
	}
	if *keyType == "" {
		panic("--type is required")
	}
	if *keyType != "RSA" && *keyType != "ECDSA" {
		panic("--type may only be RSA or ECDSA")
	}
	if *pin == "" {
		panic("--pin is required")
	}
	if *label == "" {
		panic("--label is required")
	}

	ctx := pkcs11.New(*module)
	if ctx == nil {
		panic("failed to load module")
	}
	err := ctx.Initialize()
	if err != nil {
		panic(err)
	}

	session, err := ctx.OpenSession(*slot, pkcs11.CKF_SERIAL_SESSION|pkcs11.CKF_RW_SESSION)
	if err != nil {
		panic(err)
	}

	err = ctx.Login(session, pkcs11.CKU_USER, *pin)
	if err != nil {
		panic(err)
	}

	var pubKey interface{}
	switch *keyType {
	case "RSA":
		if *rsaModLen == 0 {
			panic("--modulus-bits is required")
		}
		m, pubTmpl, privTmpl := rsaArgs(*label, *rsaModLen)
		pub, _, err := ctx.GenerateKeyPair(session, m, pubTmpl, privTmpl)
		if err != nil {
			panic(err)
		}
		pubKey = rsaPub(ctx, session, pub)
	case "ECDSA":
		if *ecdsaCurve == "" {
			panic("--ecdsaCurve is required")
		}
		curve, present := stringToCurve[*ecdsaCurve]
		if !present {
			panic("curve not supported")
		}
		m, pubTmpl, privTmpl := ecArgs(*label, curve)
		pub, _, err := ctx.GenerateKeyPair(session, m, pubTmpl, privTmpl)
		if err != nil {
			panic(err)
		}
		pubKey = ecPub(ctx, session, pub, curve)
	}

	der, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		panic(err)
	}

	err = pem.Encode(os.Stdout, &pem.Block{Type: "PUBLIC KEY", Bytes: der})
	if err != nil {
		panic(err)
	}
}
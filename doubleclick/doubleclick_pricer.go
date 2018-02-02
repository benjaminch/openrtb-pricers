package doubleclick

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"

	"github.com/benjaminch/openrtb-pricers/helpers"

	"github.com/golang/glog"
)

// DoubleClickPricer implementing price encryption and decryption
// Specs : https://developers.google.com/ad-exchange/rtb/response-guide/decrypt-price
type DoubleClickPricer struct {
	encryptionKeyRaw string
	integrityKeyRaw  string
	encryptionKey    hash.Hash
	integrityKey     hash.Hash
	keyDecodingMode  helpers.KeyDecodingMode
	scaleFactor      float64
	isDebugMode      bool
}

// NewDoubleClickPricer returns a DoubleClickPricer struct.
// Keys are either base 64 websafe of hexa. keyDecodingMode
// should be used to specify how keys should be decoded.
// Factor the clear price will be multiplied by before encryption.
// from specs, scaleFactor is 1,000,000, but you can set something else.
// Be aware that the price is stored as an int64 so depending on the digits
// precision you want, picking a scale factor smaller than 1,000,000 may lead
// to price to be rounded and loose some digits precision.
func NewDoubleClickPricer(
	encryptionKey string,
	integrityKey string,
	isBase64Keys bool,
	keyDecodingMode helpers.KeyDecodingMode,
	scaleFactor float64,
	isDebugMode bool) (*DoubleClickPricer, error) {
	var err error
	var encryptingFun, integrityFun hash.Hash

	defer glog.Flush()

	encryptingFun, err = helpers.CreateHmac(encryptionKey, isBase64Keys, keyDecodingMode)
	if err != nil {
		return nil, err
	}
	integrityFun, err = helpers.CreateHmac(integrityKey, isBase64Keys, keyDecodingMode)
	if err != nil {
		return nil, err
	}

	if isDebugMode == true {
		glog.Info("Keys decoding mode : ", keyDecodingMode)
		glog.Info("Encryption key : ", encryptionKey)
		encryptionKeyHexa, err := hex.DecodeString(encryptionKey)
		if err != nil {
			return nil, err
		}
		glog.Info("Encryption key (bytes) : ", []byte(encryptionKeyHexa))
		glog.Info("Integrity key : ", integrityKey)
		integrityKeyHexa, err := hex.DecodeString(integrityKey)
		if err != nil {
			return nil, err
		}
		glog.Info("Integrity key (bytes) : ", []byte(integrityKeyHexa))
	}

	return &DoubleClickPricer{
			encryptionKeyRaw: encryptionKey,
			integrityKeyRaw:  integrityKey,
			encryptionKey:    encryptingFun,
			integrityKey:     integrityFun,
			keyDecodingMode:  keyDecodingMode,
			scaleFactor:      scaleFactor,
			isDebugMode:      isDebugMode},
		err
}

// Encrypt encrypts a clear price and a given seed.
func (dc *DoubleClickPricer) Encrypt(
	seed string,
	price float64,
	isDebugMode bool) (string, error) {
	var err error

	// Result
	var (
		iv        [16]byte
		encoded   [8]byte
		signature [4]byte
	)

	defer glog.Flush()

	data := helpers.ApplyScaleFactor(price, dc.scaleFactor, isDebugMode)

	// Create Initialization Vector from seed
	sum := md5.Sum([]byte(seed))
	copy(iv[:], sum[:])
	if isDebugMode == true {
		glog.Info("Seed : ", seed)
		glog.Info("Initialization vector : ", iv)
	}

	//pad = hmac(e_key, iv), first 8 bytes
	pad := helpers.HmacSum(dc.encryptionKey, iv[:])[:8]
	if isDebugMode == true {
		glog.Info("// pad = hmac(e_key, iv), first 8 bytes")
		glog.Info("Pad : ", pad)
	}

	// enc_data = pad <xor> data
	for i := range data {
		encoded[i] = pad[i] ^ data[i]
	}
	if isDebugMode == true {
		glog.Info("// enc_data = pad <xor> data")
		glog.Info("Encoded price bytes : ", encoded)
	}

	// signature = hmac(i_key, data || iv), first 4 bytes
	sig := helpers.HmacSum(dc.integrityKey, append(data[:], iv[:]...))[:4]
	copy(signature[:], sig[:])
	if isDebugMode == true {
		glog.Info("// signature = hmac(i_key, data || iv), first 4 bytes")
		glog.Info("Signature : ", sig)
	}

	// final_message = WebSafeBase64Encode( iv || enc_price || signature )
	return base64.URLEncoding.EncodeToString(append(append(iv[:], encoded[:]...), signature[:]...)), err
}

// Decrypt decrypts an ecrypted price.
func (dc *DoubleClickPricer) Decrypt(encryptedPrice string, isDebugMode bool) (float64, error) {
	var err error
	var errPrice float64

	// Decode base64
	encryptedPrice = helpers.AddBase64Padding(encryptedPrice)
	decoded, err := base64.URLEncoding.DecodeString(encryptedPrice)
	if err != nil {
		return errPrice, err
	}

	if isDebugMode == true {
		glog.Info("Encrypted price : ", encryptedPrice)
		glog.Info("Base64 decoded price : ", decoded)
	}

	// Get elements
	var (
		iv         [16]byte
		p          [8]byte
		signature  [4]byte
		priceMicro [8]byte
	)

	defer glog.Flush()

	copy(iv[:], decoded[0:16])
	copy(p[:], decoded[16:24])
	copy(signature[:], decoded[24:28])

	// pad = hmac(e_key, iv)
	pad := helpers.HmacSum(dc.encryptionKey, iv[:])[:8]

	if isDebugMode == true {
		glog.Info("IV : ", hex.EncodeToString(iv[:]))
		glog.Info("Encoded price : ", hex.EncodeToString(p[:]))
		glog.Info("Signature : ", hex.EncodeToString(signature[:]))
		glog.Info("Pad : ", hex.EncodeToString(pad[:]))
	}

	// priceMicro = p <xor> pad
	for i := range p {
		priceMicro[i] = pad[i] ^ p[i]
	}

	// conf_sig = hmac(i_key, data || iv)
	sig := helpers.HmacSum(dc.integrityKey, append(priceMicro[:], iv[:]...))[:4]

	// success = (conf_sig == sig)
	for i := range sig {
		if sig[i] != signature[i] {
			return errPrice, errors.New("Failed to decrypt")
		}
	}
	price := float64(binary.BigEndian.Uint64(priceMicro[:])) / dc.scaleFactor

	return price, err
}

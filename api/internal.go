package api

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/blk-io/chimera-api/chimera"
	"github.com/blk-io/crux/utils"
	"github.com/kevinburke/nacl"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// EncryptedPayload is the struct used for storing all data associated with an encrypted
// transaction.
type EncryptedPayload struct {
	Sender         nacl.Key
	CipherText     []byte
	Nonce          nacl.Nonce
	RecipientBoxes [][]byte
	RecipientNonce nacl.Nonce
}

// PartyInfo is a struct that stores details of all enclave nodes (or parties) on the network.
type PartyInfo struct {
	url string // URL identifying this node
	// recipients map[[nacl.KeySize]byte]string // public key -> URL
	// parties    map[string]bool               // Node (or party) URLs
	recipients sync.Map // public key -> URL
	parties    sync.Map // Node (or party) URLs
	client     utils.HttpClient
	grpc       bool
}

// GetRecipient retrieves the URL associated with the provided recipient.
func (s *PartyInfo) GetRecipient(key nacl.Key) (string, bool) {
	value, ok := s.recipients.Load(*key)
	return value.(string), ok
}

func (s *PartyInfo) GetAllValues() (string, map[[nacl.KeySize]byte]string, map[string]bool) {
	recipients := make(map[[nacl.KeySize]byte]string)
	s.recipients.Range(func(key, url interface{}) bool {
		c := key.([32]byte)
		recipients[c] = url.(string)
		return true
	})
	parties := make(map[string]bool)
	s.parties.Range(func(k, v interface{}) bool {
		parties[k.(string)] = v.(bool)
		return true
	})
	return s.url, recipients, parties
}

// InitPartyInfo initializes a new PartyInfo store.
func InitPartyInfo(rawUrl string, otherNodes []string, client utils.HttpClient, grpc bool) PartyInfo {
	// parties := make(map[string]bool)
	var parties sync.Map
	for _, node := range otherNodes {
		parties.Store(node, true)
	}
	var recipients sync.Map
	return PartyInfo{
		url:        rawUrl,
		recipients: recipients,
		parties:    parties,
		client:     client,
		grpc:       grpc,
	}
}

// RegisterPublicKeys associates the provided public keys with this node.
func (s *PartyInfo) RegisterPublicKeys(pubKeys []nacl.Key) {
	for _, pubKey := range pubKeys {
		s.recipients.Store(*pubKey, s.url)
	}
}

func (s *PartyInfo) GetPartyInfoGrpc() {
	recipients := make(map[string][]byte)
	s.recipients.Range(func(key, url interface{}) bool {
		c := key.([32]byte)
		recipients[url.(string)] = c[:]
		return true
	})
	urls := make(map[string]bool)
	s.parties.Range(func(k, v interface{}) bool {
		urls[k.(string)] = v.(bool)
		return true
	})
	for rawUrl := range urls {
		if rawUrl == s.url {
			continue
		}
		var completeUrl url.URL
		url, err := completeUrl.Parse(rawUrl)
		conn, err := grpc.Dial(url.Host, grpc.WithInsecure())
		if err != nil {
			log.Errorf("Connection to gRPC server failed with error %s", err)
			continue
		}
		defer conn.Close()
		cli := chimera.NewClientClient(conn)
		if cli == nil {
			log.Errorf("Client is not intialised")
			continue
		}
		party := chimera.PartyInfo{Url: rawUrl, Recipients: recipients, Parties: urls}
		partyInfoResp, err := cli.UpdatePartyInfo(context.Background(), &party)
		if err != nil {
			log.Errorf("Error in updating party info %s", err)
			continue
		} else {
			log.Printf("Connected to the other node %s", rawUrl)
		}
		err = s.updatePartyInfoGrpc(*partyInfoResp, s.url)
		if err != nil {
			log.Errorf("Error: %s", err)
			break
		}
	}
}

// GetPartyInfo requests PartyInfo data from all remote nodes this node is aware of. The data
// provided in each response is applied to this node.
func (s *PartyInfo) GetPartyInfo() {
	if s.grpc {
		s.GetPartyInfoGrpc()
		return
	}
	encodedPartyInfo := EncodePartyInfo(*s)

	// First copy our endpoints as we update this map in place
	urls := make(map[string]bool)
	s.parties.Range(func(k, v interface{}) bool {
		urls[k.(string)] = v.(bool)
		return true
	})

	for rawUrl := range urls {
		if rawUrl == s.url {
			continue
		}

		endPoint, err := utils.BuildUrl(rawUrl, "/partyinfo")

		if err != nil {
			log.WithFields(log.Fields{"rawUrl": rawUrl, "endPoint": "/partyinfo"}).Errorf(
				"Invalid endpoint provided")
		}

		var req *http.Request
		encoded := s.getEncoded(encodedPartyInfo)
		req, err = http.NewRequest("POST", endPoint, bytes.NewBuffer(encoded))

		if err != nil {
			log.WithField("url", rawUrl).Errorf(
				"Error creating /partyinfo request, %v", err)
			break
		}
		req.Header.Set("Content-Type", "application/octet-stream")

		logRequest(req)
		resp, err := s.client.Do(req)
		if err != nil {
			log.WithField("url", rawUrl).Errorf(
				"Error sending /partyinfo request, %v", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			log.WithField("url", rawUrl).Errorf(
				"Error sending /partyinfo request, non-200 status code: %v", resp)
			continue
		}

		err = s.updatePartyInfo(resp, rawUrl)

		if err != nil {
			break
		}
	}
}

func (s *PartyInfo) updatePartyInfoGrpc(partyInfoReq chimera.PartyInfoResponse, rawUrl string) error {
	pi, err := DecodePartyInfo(partyInfoReq.Payload)
	// log.Println("-------------Update info------------------")
	// for key, value := range pi.recipients {
	// 	log.Println(hex.EncodeToString(key[:]), value)
	// }
	if err != nil {
		log.WithField("url", rawUrl).Errorf(
			"Unable to decode partyInfo response from host, %v", err)
		return err
	}
	recipients := make(map[[nacl.KeySize]byte]string)
	pi.recipients.Range(func(key, url interface{}) bool {
		c := key.([32]byte)
		recipients[c] = url.(string)
		return true
	})
	parties := make(map[string]bool)
	pi.parties.Range(func(k, v interface{}) bool {
		parties[k.(string)] = v.(bool)
		return true
	})
	s.UpdatePartyInfoGrpc(pi.url, recipients, parties)
	return nil
}

func (s *PartyInfo) updatePartyInfo(resp *http.Response, rawUrl string) error {
	var encoded []byte
	encoded, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.WithField("url", rawUrl).Errorf(
			"Unable to read partyInfo response from host, %v", err)
		return err
	}
	s.UpdatePartyInfo(encoded)
	return nil
}

func (s *PartyInfo) getEncoded(encodedPartyInfo []byte) []byte {
	if s.grpc {
		recipients := make(map[string][]byte)
		s.recipients.Range(func(key, url interface{}) bool {
			copy(recipients[url.(string)], key.([]byte))
			return true
		})
		urls := make(map[string]bool)
		s.parties.Range(func(k, v interface{}) bool {
			urls[k.(string)] = v.(bool)
			return true
		})
		e, err := json.Marshal(UpdatePartyInfo{s.url, recipients, urls})
		if err != nil {
			log.Errorf("Marshalling failed %v", err)
			return nil
		}
		return e
	}
	return encodedPartyInfo[:]
}

func (s *PartyInfo) PollPartyInfo() {
	time.Sleep(time.Duration(rand.Intn(16)) * time.Second)
	s.GetPartyInfo()

	ticker := time.NewTicker(2 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				s.GetPartyInfo()
			case <-quit:
				ticker.Stop()
				return
			}
		}
	}()
}

// UpdatePartyInfo updates the PartyInfo datastore with the provided encoded data.
// This can happen from the /partyinfo server endpoint being hit, or by a response from us hitting
// another nodes /partyinfo endpoint.
// TODO: Control access via a channel for updates.
func (s *PartyInfo) UpdatePartyInfo(encoded []byte) {
	log.Debugf("Updating party info payload: %s", hex.EncodeToString(encoded))
	pi, err := DecodePartyInfo(encoded)

	if err != nil {
		log.WithField("encoded", encoded).Errorf(
			"Unable to decode party info, error: %v", err)
	}
	pi.parties.Range(func(k, v interface{}) bool {
		s.parties.Store(k.(string), v.(bool))
		return true
	})
	pi.recipients.Range(func(publicKey, url interface{}) bool {
		// we should ignore messages about ourselves
		// in order to stop people masquerading as you, there
		// should be a digital signature associated with each
		// url -> node broadcast
		if url != s.url {
			c := publicKey.([32]byte)
			s.recipients.Store(c, url.(string))
		}
		return true
	})
}

func (s *PartyInfo) UpdatePartyInfoGrpc(url string, recipients map[[nacl.KeySize]byte]string, parties map[string]bool) {
	for publicKey, url := range recipients {
		// we should ignore messages about ourselves
		// in order to stop people masquerading as you, there
		// should be a digital signature associated with each
		// url -> node broadcast
		if url != s.url {
			s.recipients.Store(publicKey, url)
		}
	}
	for url := range parties {
		// we don't want to broadcast party info to ourselves
		s.parties.Store(url, true)
	}
	// _, rep, _ := s.GetAllValues()
	// log.Println("-------------update end------------------")
	// for key, value := range rep {
	// 	log.Println(hex.EncodeToString(key[:]), value)
	// }
}

func PushGrpc(encoded []byte, path string, epl EncryptedPayload) error {
	var completeUrl url.URL
	url, err := completeUrl.Parse(path)
	conn, err := grpc.Dial(url.Host, grpc.WithInsecure())
	if err != nil {
		log.Fatalf("Connection to gRPC server failed with error %s", err)
	}
	defer conn.Close()
	cli := chimera.NewClientClient(conn)
	if cli == nil {
		log.Fatalf("Client is not intialised")
	}

	var sender [32]byte
	var nonce [32]byte
	var recipientNonce [32]byte

	copy(sender[:], (*epl.Sender)[:])
	copy(nonce[:], (*epl.Nonce)[:])
	copy(recipientNonce[:], (*epl.RecipientNonce)[:])
	encrypt := chimera.EncryptedPayload{
		Sender:          sender[:],
		CipherText:      epl.CipherText,
		Nonce:           nonce[:],
		ReciepientNonce: recipientNonce[:],
		ReciepientBoxes: epl.RecipientBoxes,
	}
	pushPayload := chimera.PushPayload{Ep: &encrypt, Encoded: encoded}
	_, err = cli.Push(context.Background(), &pushPayload)
	if err != nil {
		log.Errorf("Push failed with %s", err)
		return err
	}
	return nil
}

// Push is responsible for propagating the encoded payload to the given remote node.
func Push(encoded []byte, url string, client utils.HttpClient) (string, error) {

	endPoint, err := utils.BuildUrl(url, "/push")
	if err != nil {
		return "", err
	}

	var req *http.Request
	req, err = http.NewRequest("POST", endPoint, bytes.NewReader(encoded))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	logRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("non-200 status code received: %v", resp)
	}

	body, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return "", err
	}

	return string(body), nil
}

func logRequest(r *http.Request) {
	if log.GetLevel() == log.DebugLevel {
		dump, err := httputil.DumpRequestOut(r, true)
		if err != nil {
			log.Fatal(err)
		}

		log.Debugf("%q", dump)
	}
}

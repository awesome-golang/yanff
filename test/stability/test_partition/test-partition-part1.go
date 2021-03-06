// Copyright 2017 Intel Corporation.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"crypto/md5"
	"flag"
	"github.com/intel-go/yanff/flow"
	"github.com/intel-go/yanff/packet"
	"sync"
	"sync/atomic"
	"unsafe"
)

// test-partition-part1: sends packets to 0 port, receives from 0 and 1 ports.
// This part of test generates empty packets and send to 0 port. For each packet sender
// calculates md5 hash sum from all headers, write it to packet.Data and check it on packet receive.
// This part of test expects to get approximately 90% of packet on 0 port and ~10% packets on 1 port.
// Test also calculates number of broken packets and prints it when
// a predefined number of packets is received.
//
// test-partition-part2:
// This part of test receives packets on 0 port, use partition function to create second flow.
// First 1000 received packets stay in this flow, next 100 go to new flow, and so on.

const (
	TOTAL_PACKETS = 10000000

	// Test expects to receive ~90% of packets on 0 port and ~10% on 1 port
	// Test is PASSSED, if p1 is in [LOW1;HIGH1] and p2 in [LOW2;HIGH2]
	eps   = 3
	HIGH1 = 90 + eps
	LOW1  = 90 - eps
	HIGH2 = 10 + eps
	LOW2  = 10 - eps
)

var (
	// Payload is 16 byte md5 hash sum of headers
	PAYLOAD_SIZE uint   = 16
	SPEED        uint64 = 1000
	PASSED_LIMIT uint64 = 85

	sent          uint64     = 0
	recvPackets   uint64     = 0
	recvOnPort0   uint64     = 0
	recvOnPort1   uint64     = 0
	brokenPackets uint64     = 0
	testDoneEvent *sync.Cond = nil
)

func main() {
	flag.Uint64Var(&PASSED_LIMIT, "PASSED_LIMIT", PASSED_LIMIT, "received/sent minimum ratio to pass test")
	flag.Uint64Var(&SPEED, "SPEED", SPEED, "speed of generator, Pkts/s")

	// Init YANFF system at 16 available cores
	flow.SystemInit(16)

	var m sync.Mutex
	testDoneEvent = sync.NewCond(&m)

	// Create output packet flow
	outputFlow := flow.SetGenerator(generatePacket, SPEED, nil)
	flow.SetSender(outputFlow, 0)

	// Create receiving flows and set a checking function for it
	inputFlow0 := flow.SetReceiver(0)
	flow.SetHandler(inputFlow0, checkPacketsOn0Port, nil)

	inputFlow1 := flow.SetReceiver(1)
	flow.SetHandler(inputFlow1, checkPacketsOn1Port, nil)

	flow.SetStopper(inputFlow0)
	flow.SetStopper(inputFlow1)

	// Start pipeline
	go flow.SystemStart()

	// Wait for enough packets to arrive
	testDoneEvent.L.Lock()
	testDoneEvent.Wait()
	testDoneEvent.L.Unlock()

	// Compose statistics
	recv0 := atomic.LoadUint64(&recvOnPort0)
	recv1 := atomic.LoadUint64(&recvOnPort1)
	received := recv0 + recv1

	var p1 int
	var p2 int
	if received != 0 {
		p1 = int(recv0 * 100 / received)
		p2 = int(recv1 * 100 / received)
	}
	broken := atomic.LoadUint64(&brokenPackets)

	// Print report
	println("Sent", sent, "packets")
	println("Received", received, "packets")

	println("Proportion of packets received on 0 port ", p1, "%")
	println("Proportion of packets received on 1 port ", p2, "%")

	println("Broken = ", broken, "packets")

	if p1 <= HIGH1 && p2 <= HIGH2 && p1 >= LOW1 && p2 >= LOW2 && received*100/sent > PASSED_LIMIT {
		println("TEST PASSED")
	} else {
		println("TEST FAILED")
	}

}

// Generate packets
func generatePacket(pkt *packet.Packet, context flow.UserContext) {
	packet.InitEmptyEtherIPv4UDPPacket(pkt, PAYLOAD_SIZE)
	if pkt == nil {
		panic("Failed to create new packet")
	}

	// Extract headers of packet
	headerSize := uintptr(pkt.Data) - pkt.Unparsed
	hdrs := (*[1000]byte)(unsafe.Pointer(pkt.Unparsed))[0:headerSize]
	ptr := (*PacketData)(pkt.Data)
	ptr.HdrsMD5 = md5.Sum(hdrs)

	atomic.AddUint64(&sent, 1)
}

// Count and check packets received on 0 port
func checkPacketsOn0Port(pkt *packet.Packet, context flow.UserContext) {
	recvCount := atomic.AddUint64(&recvPackets, 1)

	offset := pkt.ParseL4Data()
	if offset < 0 {
		println("ParseL4Data returned negative value", offset)
		// Some received packets are not generated by this example
		// They cannot be parsed due to unknown protocols, skip them
	} else {
		ptr := (*PacketData)(pkt.Data)

		// Recompute hash to check how many packets are valid
		headerSize := uintptr(pkt.Data) - pkt.Unparsed
		hdrs := (*[1000]byte)(unsafe.Pointer(pkt.Unparsed))[0:headerSize]
		hash := md5.Sum(hdrs)

		if hash != ptr.HdrsMD5 {
			// Packet is broken
			atomic.AddUint64(&brokenPackets, 1)
			return
		}
		atomic.AddUint64(&recvOnPort0, 1)
	}

	if recvCount >= TOTAL_PACKETS {
		testDoneEvent.Signal()
	}
}

// Count and check packets received on 1 port
func checkPacketsOn1Port(pkt *packet.Packet, context flow.UserContext) {
	recvCount := atomic.AddUint64(&recvPackets, 1)

	offset := pkt.ParseL4Data()
	if offset < 0 {
		println("ParseL4Data returned negative value", offset)
		// Some received packets are not generated by this example
		// They cannot be parsed due to unknown protocols, skip them
	} else {
		ptr := (*PacketData)(pkt.Data)

		// Recompute hash to check how many packets are valid
		headerSize := uintptr(pkt.Data) - pkt.Unparsed
		hdrs := (*[1000]byte)(unsafe.Pointer(pkt.Unparsed))[0:headerSize]
		hash := md5.Sum(hdrs)

		if hash != ptr.HdrsMD5 {
			// Packet is broken
			atomic.AddUint64(&brokenPackets, 1)
			return
		}
		atomic.AddUint64(&recvOnPort1, 1)
	}
	if recvCount >= TOTAL_PACKETS {
		testDoneEvent.Signal()
	}
}

type PacketData struct {
	HdrsMD5 [16]byte
}

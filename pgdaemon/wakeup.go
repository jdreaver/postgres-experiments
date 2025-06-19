package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"time"
)

// WakeupPacket is sent to other nodes to wake them up so they don't
// wait for the next reconciliation cycle
type WakeupPacket struct {
	ClusterName string `json:"cluster_name"`
	SenderNode  string `json:"sender_node"`
}

// WakeupManager handles sending and receiving wakeup packets
type WakeupManager struct {
	port        int
	clusterName string
	nodeName    string
	wakeupChan  chan struct{}
}

func NewWakeupManager(port int, clusterName, nodeName string) *WakeupManager {
	return &WakeupManager{
		port:        port,
		clusterName: clusterName,
		nodeName:    nodeName,
		wakeupChan:  make(chan struct{}, 1), // Buffered to avoid blocking
	}
}

func (w *WakeupManager) StartListener(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf(":%d", w.port))
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP port %d: %w", w.port, err)
	}

	go func() {
		defer conn.Close()

		buffer := make([]byte, 1024)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				conn.SetReadDeadline(time.Now().Add(1 * time.Second))
				n, _, err := conn.ReadFromUDP(buffer)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue // Timeout is expected, continue listening
					}
					log.Printf("Error reading UDP packet: %v", err)
					continue
				}

				var packet WakeupPacket
				if err := json.Unmarshal(buffer[:n], &packet); err != nil {
					log.Printf("Failed to unmarshal wakeup packet: %v", err)
					continue
				}

				// Validate packet
				if packet.ClusterName != w.clusterName {
					log.Printf("WARN: Received wakeup packet from node %s for wrong cluster: %+v", packet.SenderNode, packet)
					continue // Ignore packets from other clusters
				}
				if packet.SenderNode == w.nodeName {
					log.Printf("WARN: Received wakeup packet from current node: %+v", packet)
					continue // Ignore packets from self
				}

				// Send wakeup signal (non-blocking)
				select {
				case w.wakeupChan <- struct{}{}:
					log.Printf("Received wakeup from node %s", packet.SenderNode)
				default:
					// Channel is full, wakeup already pending
				}
			}
		}
	}()

	return nil
}

func (w *WakeupManager) WakeupChannel() <-chan struct{} {
	return w.wakeupChan
}

func (w *WakeupManager) SendWakeupToNodes(peerHostnames []string) {
	packet := WakeupPacket{
		ClusterName: w.clusterName,
		SenderNode:  w.nodeName,
	}

	data, err := json.Marshal(packet)
	if err != nil {
		log.Printf("Failed to marshal wakeup packet: %v", err)
		return
	}

	for _, hostname := range peerHostnames {
		if hostname == "" {
			continue
		}

		go func(host string) {
			addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", host, w.port))
			if err != nil {
				log.Printf("Failed to resolve UDP address for %s: %v", host, err)
				return
			}

			conn, err := net.DialUDP("udp", nil, addr)
			if err != nil {
				log.Printf("Failed to connect to %s: %v", host, err)
				return
			}
			defer conn.Close()

			conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
			if _, err := conn.Write(data); err != nil {
				log.Printf("Failed to send wakeup to %s: %v", host, err)
				return
			}

			log.Printf("Sent wakeup to node %s", host)
		}(hostname)
	}
}

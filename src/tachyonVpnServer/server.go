package tachyonVpnClient

import (
	"crypto/tls"
	"fmt"
	"github.com/tachyon-protocol/udw/udwBinary"
	"github.com/tachyon-protocol/udw/udwBytes"
	"github.com/tachyon-protocol/udw/udwConsole"
	"github.com/tachyon-protocol/udw/udwErr"
	"github.com/tachyon-protocol/udw/udwIpPacket"
	"github.com/tachyon-protocol/udw/udwLog"
	"github.com/tachyon-protocol/udw/udwNet"
	"github.com/tachyon-protocol/udw/udwNet/udwTapTun"
	"github.com/tachyon-protocol/udw/udwRand"
	"github.com/tachyon-protocol/udw/udwTlsSelfSignCertV2"
	"net"
	"strconv"
	"sync"
	"tachyonSimpleVpnProtocol"
)

type vpnClient struct {
	id          uint64
	vpnIpOffset int
	vpnIp       net.IP

	locker sync.Mutex
	conn   net.Conn
}

var (
	gLocker         sync.Mutex
	gClientMap      map[uint64]*vpnClient
	gVpnIpList      [maxCountVpnIp]*vpnClient
	gNextVpnIpIndex int
)

type ServerRunReq struct {
	UseRelay      bool
	RelayServerIp string
}

func ServerRun(req ServerRunReq) {
	clientId := tachyonSimpleVpnProtocol.GetClientId()
	fmt.Println("ClientId:", clientId)

	tun, err := udwTapTun.NewTun("")
	udwErr.PanicIfError(err)
	err = udwTapTun.SetP2PIpAndUp(udwTapTun.SetP2PIpRequest{
		IfaceName: tun.Name(),
		SrcIp:     udwNet.Ipv4AddAndCopyWithBuffer(READONLY_vpnIpStart, 2, nil),
		DstIp:     udwNet.Ipv4AddAndCopyWithBuffer(READONLY_vpnIpStart, 1, nil),
		Mtu:       tachyonSimpleVpnProtocol.Mtu,
		Mask:      net.CIDRMask(16, 32),
	})
	udwErr.PanicIfError(err)
	networkConfig()
	fmt.Println("Server started ✔")
	go func() {
		bufR := make([]byte, 3<<20)
		bufW := udwBytes.NewBufWriter(nil)
		for {
			n, err := tun.Read(bufR)
			udwErr.PanicIfError(err)
			packetBuf := bufR[:n]
			ipPacket, errMsg := udwIpPacket.NewIpv4PacketFromBuf(packetBuf)
			if errMsg != "" {
				//noinspection SpellCheckingInspection
				udwLog.Log("[psmddnegwg] TUN Read parse IPv4 failed", errMsg)
				return
			}
			ip := ipPacket.GetDstIp()
			client := getClientByVpnIp(ip)
			if client == nil {
				//noinspection SpellCheckingInspection
				udwLog.Log("[rdtp9rk84m] TUN Read no such client")
				continue
			}
			responseVpnPacket := &tachyonSimpleVpnProtocol.VpnPacket{
				ClientIdFrom: clientId,
				Cmd:          tachyonSimpleVpnProtocol.CmdData,
			}
			ipPacket.SetDstIp__NoRecomputeCheckSum(READONLY_vpnIpClient)
			ipPacket.TcpFixMss__NoRecomputeCheckSum(tachyonSimpleVpnProtocol.Mss)
			ipPacket.RecomputeCheckSum()
			responseVpnPacket.Data = ipPacket.SerializeToBuf()
			bufW.Reset()
			responseVpnPacket.Encode(bufW)
			_ = udwBinary.WriteByteSliceWithUint32LenNoAllocV2(client.conn, bufW.GetBytes()) //TODO
		}
	}()

	certs := []tls.Certificate{
		*udwTlsSelfSignCertV2.GetTlsCertificate(),
	}
	if req.UseRelay {
		vpnConn, err := net.Dial("tcp", req.RelayServerIp+":"+strconv.Itoa(tachyonSimpleVpnProtocol.VpnPort))
		udwErr.PanicIfError(err)
		fmt.Println("Server connected to relay server[", req.RelayServerIp, "] ✔")
		vpnConn = tls.Client(vpnConn, &tls.Config{
			ServerName:         udwRand.MustCryptoRandToReadableAlpha(5) + ".com",
			InsecureSkipVerify: true,
			NextProtos:         []string{"http/1.1", "h2"},
		})
		go func() {
			vpnPacket := &tachyonSimpleVpnProtocol.VpnPacket{}
			buf := udwBytes.NewBufWriter(nil)
			for {
				err := udwBinary.ReadByteSliceWithUint32LenToBufW(vpnConn, buf)
				udwErr.PanicIfError(err)
				err  = vpnPacket.Decode(buf.GetBytes())
				udwErr.PanicIfError(err)
				if vpnPacket.Cmd == tachyonSimpleVpnProtocol.CmdForward {
					if vpnPacket.ClientIdForwardTo == clientId {

					} else {
						fmt.Println("[vw9tm9rv2s] not forward to self")
					}
				} else {
					fmt.Println("[d39e7d859m]Unexpected Cmd[",vpnPacket.Cmd,"]")
				}
			}
		}()
	} else {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(tachyonSimpleVpnProtocol.VpnPort))
		udwErr.PanicIfError(err)
		go func() {
			for {
				conn, err := ln.Accept()
				udwErr.PanicIfError(err)
				if tachyonSimpleVpnProtocol.Debug {
					udwLog.Log("New Conn", conn.RemoteAddr())
				}
				conn = tls.Server(conn, &tls.Config{
					Certificates: certs,
					NextProtos:   []string{"http/1.1"},
				})
				go func() {
					bufR := make([]byte, 3<<20)
					vpnPacket := &tachyonSimpleVpnProtocol.VpnPacket{}
					vpnIpBufW := udwBytes.NewBufWriter(nil)
					for {
						out, err := udwBinary.ReadByteSliceWithUint32LenNoAllocLimitMaxSize(conn, bufR, uint32(len(bufR)))
						if err != nil {
							_ = conn.Close()
							return
						}
						err = vpnPacket.Decode(out)
						if err != nil {
							_ = conn.Close()
							return
						}
						client := getClient(vpnPacket.ClientIdFrom, conn)
						ipPacket, errMsg := udwIpPacket.NewIpv4PacketFromBuf(vpnPacket.Data)
						if errMsg != "" {
							_ = conn.Close()
							return
						}
						vpnIp := udwNet.Ipv4AddAndCopyWithBuffer(READONLY_vpnIpStart, uint32(client.vpnIpOffset), vpnIpBufW)
						ipPacket.SetSrcIp__NoRecomputeCheckSum(vpnIp)
						ipPacket.TcpFixMss__NoRecomputeCheckSum(tachyonSimpleVpnProtocol.Mss)
						ipPacket.RecomputeCheckSum()
						_, err = tun.Write(ipPacket.SerializeToBuf())
						if err != nil {
							_ = conn.Close()
							return
						}
					}
				}()
			}
		}()
	}
	udwConsole.WaitForExit()
}
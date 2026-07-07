package main

import (
	"encoding/binary"
	"fmt"
	"strconv"

	"github.com/hyperledger/fabric-chaincode-go/shim"
	pb "github.com/hyperledger/fabric-protos-go/peer"
)

func int64BE(n int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(n))
	return b
}

type CC struct{}

func (c *CC) Init(stub shim.ChaincodeStubInterface) pb.Response { return shim.Success(nil) }

func (c *CC) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	fn, args := stub.GetFunctionAndParameters()
	switch fn {
	case "Set":
		return c.set(stub, args)
	case "Credit":
		return c.append(stub, args, +1)
	case "Debit":
		return c.append(stub, args, -1)
	case "Get":
		return c.get(stub, args)
	default:
		return shim.Error("unknown function: " + fn)
	}
}

func (c *CC) set(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: Set(acct, B)")
	}
	b, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return shim.Error("B must be int64")
	}
	if err := stub.PutState(args[0]+"~init", int64BE(b)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (c *CC) append(stub shim.ChaincodeStubInterface, args []string, sign int64) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: Credit|Debit(acct, n)")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n < 0 {
		return shim.Error("n must be a non-negative int64")
	}
	key := args[0] + "~" + stub.GetTxID()
	if err := stub.PutState(key, int64BE(sign*n)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (c *CC) get(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("usage: Get(acct)")
	}
	start := args[0] + "~"
	end := args[0] + "~" + string(rune(0xFFFF))
	iter, err := stub.GetStateByRange(start, end)
	if err != nil {
		return shim.Error(err.Error())
	}
	defer iter.Close()
	var sum int64
	for iter.HasNext() {
		kv, err := iter.Next()
		if err != nil {
			return shim.Error(err.Error())
		}
		if len(kv.Value) == 8 {
			sum += int64(binary.BigEndian.Uint64(kv.Value))
		}
	}
	return shim.Success([]byte(strconv.FormatInt(sum, 10)))
}

func main() {
	if err := shim.Start(new(CC)); err != nil {
		fmt.Printf("Error starting high-throughput cc: %s\n", err)
	}
}

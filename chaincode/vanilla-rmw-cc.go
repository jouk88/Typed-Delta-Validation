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

func decodeBE(b []byte) (int64, error) {
	if len(b) != 8 {
		return 0, fmt.Errorf("expected 8-byte int64, got %d", len(b))
	}
	return int64(binary.BigEndian.Uint64(b)), nil
}

type CC struct{}

func (c *CC) Init(stub shim.ChaincodeStubInterface) pb.Response { return shim.Success(nil) }

func (c *CC) Invoke(stub shim.ChaincodeStubInterface) pb.Response {
	fn, args := stub.GetFunctionAndParameters()
	switch fn {
	case "Set":
		return c.set(stub, args)
	case "Credit":
		return c.rmw(stub, args, +1)
	case "Debit":
		return c.rmw(stub, args, -1)
	case "Get":
		return c.get(stub, args)
	default:
		return shim.Error("unknown function: " + fn)
	}
}

func (c *CC) set(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: Set(key, n)")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil {
		return shim.Error("n must be int64")
	}
	if err := stub.PutState(args[0], int64BE(n)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (c *CC) rmw(stub shim.ChaincodeStubInterface, args []string, sign int64) pb.Response {
	if len(args) != 2 {
		return shim.Error("usage: Credit|Debit(key, n)")
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || n < 0 {
		return shim.Error("n must be a non-negative int64")
	}
	cur, err := stub.GetState(args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	var bal int64
	if len(cur) != 0 {
		if bal, err = decodeBE(cur); err != nil {
			return shim.Error(err.Error())
		}
	}
	next := bal + sign*n
	if next < 0 {
		return shim.Error("insufficient funds")
	}
	if err := stub.PutState(args[0], int64BE(next)); err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success(nil)
}

func (c *CC) get(stub shim.ChaincodeStubInterface, args []string) pb.Response {
	if len(args) != 1 {
		return shim.Error("usage: Get(key)")
	}
	v, err := stub.GetState(args[0])
	if err != nil {
		return shim.Error(err.Error())
	}
	if v == nil {
		return shim.Error("key not found: " + args[0])
	}
	bal, err := decodeBE(v)
	if err != nil {
		return shim.Error(err.Error())
	}
	return shim.Success([]byte(strconv.FormatInt(bal, 10)))
}

func main() {
	if err := shim.Start(new(CC)); err != nil {
		fmt.Printf("Error starting vanilla RMW cc: %s\n", err)
	}
}

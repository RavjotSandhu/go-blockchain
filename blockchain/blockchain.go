package blockchain

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/dgraph-io/badger"
)

const (
	path        = ".tmp/blocks"
	file        = ".tmp/blocks/MANIFEST"
	genesisData = "First Transaction from Genesis" //arbitrary data for our implementation(arbirary input signature)
)

type Blockchain struct {
	LastHash []byte
	Database *badger.DB
}

type BlockchainIterator struct {
	CurrentHash []byte
	Database    *badger.DB
}

func DBexists() bool {
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return false
	}
	return true
}

func InitBlockchain(address string) *Blockchain {
	//check wether the database exists
	if DBexists() {
		fmt.Println("Blockchain already exists")
		runtime.Goexit()
	}

	var lastHash []byte
	//with this option struct we want to specify where we want our database files to be stored
	opts := badger.DefaultOptions(path)
	opts.Dir = path      //stores keys and meta data
	opts.ValueDir = path //here database stores all of the values but here it does not matter because the folder is same

	db, err := badger.Open(opts) //it returns a tuple with a pointer to the database and an error
	Handle(err)

	err = db.Update(func(txn *badger.Txn) error {
		/*
			check if there is a blockchain already stored or not
			if there is already a blockchainthen we will create a new blockchain instance in memory and we will get the
			last hash of our blockchian in our disk database and we will push to this instance in memory
			the reason why the last hash is important is that it helps derive a new block in our blockchain
			if there is no existing blockchain in our we will create a genesis block,store it in the database,then we will save the genesis block's hash as the lastblock hash in our database
			then we will create a new blockchain instance with lasthash pointing towards the genesis block
		*/
		cbtx := CoinbaseTx(address, genesisData)
		genesis := Genesis(cbtx)
		fmt.Println("Genesis proved and created")
		err = txn.Set(genesis.Hash, genesis.Serialize())
		Handle(err)
		err = txn.Set([]byte("lh"), genesis.Hash)
		lastHash = genesis.Hash
		return err
	})
	Handle(err)
	blockchain := Blockchain{lastHash, db}
	return &blockchain
}

//other part of initblockchain function
func ContinueBlockChain(address string) *Blockchain {
	if DBexists() == false {
		fmt.Println("No existing blockchain found, create one!")
		runtime.Goexit()
	}
	var lastHash []byte
	opts := badger.DefaultOptions(path)
	opts.Dir = path
	opts.ValueDir = path

	db, err := badger.Open(opts)
	Handle(err)

	err = db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("lh"))
		Handle(err)
		err1 := item.Value(func(val []byte) error {
			lastHash = append([]byte{}, val...)
			return nil
		})
		Handle(err1)
		return err
	})
	Handle(err)
	chain := Blockchain{lastHash, db}
	return &chain
} //now we can easily create the functionality that we need for our command line to be able to check the amt of tokens that are assigned to an account as well as be able to send tokens from one account to the next

func (chain *Blockchain) AddBlock(transactions []*Transaction) *Block {
	var lastHash []byte

	for _, tx := range transactions {
		if chain.VerifyTransaction(tx) != true {
			log.Panic("Invalid Transaction")
		}
	}
	err := chain.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte("lh"))
		Handle(err)
		err1 := item.Value(func(val []byte) error {
			lastHash = append([]byte{}, val...)
			return nil
		})
		Handle(err1)
		return err
	})
	Handle(err)

	newBlock := CreateBlock(transactions, lastHash)
	err = chain.Database.Update(func(txn *badger.Txn) error {
		err := txn.Set(newBlock.Hash, newBlock.Serialize())
		Handle(err)
		err = txn.Set([]byte("lh"), newBlock.Hash)
		chain.LastHash = newBlock.Hash
		return err
	})
	Handle(err)
	return newBlock
}

//converting the Blockchain struct into the BlockchainIterator struct
func (chain *Blockchain) Iterator() *BlockchainIterator {
	iter := &BlockchainIterator{chain.LastHash, chain.Database}
	return iter
}

func (iter *BlockchainIterator) Next() *Block {
	var block *Block

	err := iter.Database.View(func(txn *badger.Txn) error {
		item, err := txn.Get(iter.CurrentHash)
		Handle(err)
		var encodedBlock []byte
		err1 := item.Value(func(val []byte) error {
			encodedBlock = append([]byte{}, val...)
			return nil
		})
		Handle(err1)
		block = Deserialize(encodedBlock)
		return err
	})
	Handle(err)
	iter.CurrentHash = block.PrevHash
	return block
}

/*
unspent transactions are those that have an output not referenced by other inputs
these are important because if an output has not been spent that means that tokens still exist for a certain user
So by counting all unspent outputs that are assigned to a certain user we can find that how many tokens are assigned to that user*/
func (chain *Blockchain) FindUTXO() map[string]TxOutputs {
	UTXO := make(map[string]TxOutputs)
	spentTxs := make(map[string][]int)
	iter := chain.Iterator()

	for {
		block := iter.Next()
		for _, tx := range block.Transactions {
			txID := hex.EncodeToString(tx.ID)

		Outputs:
			for outIdx, out := range tx.Outputs {
				if spentTxs[txID] != nil {
					for _, spentOut := range spentTxs[txID] {
						if spentOut == outIdx {
							continue Outputs
						}
					}
				}
				outs := UTXO[txID]
				outs.Outputs = append(outs.Outputs, out)
				UTXO[txID] = outs
			}
			if tx.IsCoinbase() == false {
				for _, in := range tx.Inputs {
					inTxID := hex.EncodeToString(in.ID)
					spentTxs[inTxID] = append(spentTxs[inTxID], in.Out)
				}
			}
		}
		if len(block.PrevHash) == 0 {
			break
		}
	}
	return UTXO
}

func (bc *Blockchain) FindTransaction(ID []byte) (Transaction, error) {
	iter := bc.Iterator()
	for {
		block := iter.Next()
		for _, tx := range block.Transactions {
			if bytes.Compare(tx.ID, ID) == 0 {
				return *tx, nil
			}
		}
		if len(block.PrevHash) == 0 {
			break
		}
	}
	return Transaction{}, errors.New("Transaction does not exist")
}

func (bc *Blockchain) SignTransaction(tx *Transaction, prevKey ecdsa.PrivateKey) {
	prevTXs := make(map[string]Transaction)
	for _, in := range tx.Inputs {
		prevTX, err := bc.FindTransaction(in.ID)
		Handle(err)
		prevTXs[hex.EncodeToString(prevTX.ID)] = prevTX
	}
	tx.Sign(prevKey, prevTXs)
}

func (bc *Blockchain) VerifyTransaction(tx *Transaction) bool {
	if tx.IsCoinbase() {
		return true
	}
	prevTXs := make(map[string]Transaction)
	for _, in := range tx.Inputs {
		prevTX, err := bc.FindTransaction(in.ID)
		Handle(err)
		prevTXs[hex.EncodeToString(prevTX.ID)] = prevTX
	}
	return tx.Verify(prevTXs)
}

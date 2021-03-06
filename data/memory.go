package data

import (
	"sort"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"
)

// buckets:
//  - objects hash -> []byte (including schemas)
//  - roots   hash -> encoded RootPack
//  - feeds   pubkey -> { seq -> hash of root }
type memoryDB struct {
	mx      sync.RWMutex
	objects map[cipher.SHA256][]byte
	roots   map[cipher.SHA256]*RootPack
	feeds   map[cipher.PubKey]map[uint64]cipher.SHA256
}

// NewMemoryDB creates new database in memory
func NewMemoryDB() (db DB) {
	db = &memoryDB{
		objects: make(map[cipher.SHA256][]byte),
		roots:   make(map[cipher.SHA256]*RootPack),
		feeds:   make(map[cipher.PubKey]map[uint64]cipher.SHA256),
	}
	return
}

func (d *memoryDB) Del(key cipher.SHA256) {
	d.mx.Lock()
	defer d.mx.Unlock()

	delete(d.objects, key)
}

func (d *memoryDB) Get(key cipher.SHA256) (value []byte, ok bool) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	value, ok = d.objects[key] // unsafe
	return
}

func (d *memoryDB) Set(key cipher.SHA256, value []byte) {
	d.mx.Lock()
	defer d.mx.Unlock()

	d.objects[key] = value
}

func (d *memoryDB) Add(value []byte) (key cipher.SHA256) {
	key = cipher.SumSHA256(value)
	d.Set(key, value)
	return
}

func (d *memoryDB) IsExist(key cipher.SHA256) (ok bool) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	_, ok = d.objects[key]
	return
}

func (d *memoryDB) Range(fn func(key cipher.SHA256, value []byte) (stop bool)) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	for key, value := range d.objects {
		if fn(key, value) {
			return
		}
	}
	return
}

func (d *memoryDB) RangeDelete(fn func(key cipher.SHA256) (del bool)) {
	d.mx.Lock()
	defer d.mx.Unlock()

	for key := range d.objects {
		if fn(key) {
			delete(d.objects, key)
		}
	}
	return
}

//
// Feeds
//

func (d *memoryDB) AddFeed(pk cipher.PubKey) {
	d.mx.Lock()
	defer d.mx.Unlock()

	if _, ok := d.feeds[pk]; !ok {
		d.feeds[pk] = map[uint64]cipher.SHA256{}
	}
}

func (d *memoryDB) HasFeed(pk cipher.PubKey) (has bool) {
	d.mx.Lock()
	defer d.mx.Unlock()

	_, has = d.feeds[pk]
	return
}

func (d *memoryDB) Feeds() (fs []cipher.PubKey) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	if len(d.feeds) == 0 {
		return
	}
	fs = make([]cipher.PubKey, 0, len(d.feeds))
	for pk := range d.feeds {
		fs = append(fs, pk)
	}
	return
}

func (d *memoryDB) DelFeed(pk cipher.PubKey) {
	d.mx.Lock()
	defer d.mx.Unlock()

	for _, hash := range d.feeds[pk] {
		delete(d.roots, hash)
	}
	delete(d.feeds, pk)
}

func (d *memoryDB) AddRoot(pk cipher.PubKey, rr *RootPack) (err error) {
	rp := new(RootPack)
	*rp = *rr // copy (required)
	// test given rp
	if rp.Seq == 0 {
		if rp.Prev != (cipher.SHA256{}) {
			err = newRootError(pk, rp, "unexpected prev. reference")
			return
		}
	} else if rp.Prev == (cipher.SHA256{}) {
		err = newRootError(pk, rp, "missing prev. reference")
		return
	}
	if rp.Hash != cipher.SumSHA256(rp.Root) {
		err = newRootError(pk, rp, "wrong hash of the root")
		return
	}

	d.mx.Lock()
	defer d.mx.Unlock()

	//
	var ok bool
	var roots map[uint64]cipher.SHA256
	if roots, ok = d.feeds[pk]; !ok {
		d.feeds[pk] = map[uint64]cipher.SHA256{
			rp.Seq: rp.Hash,
		}
		d.roots[rp.Hash] = rp
		return
	}
	// ok
	if _, ok = roots[rp.Seq]; ok {
		err = ErrRootAlreadyExists
		return
	}
	roots[rp.Seq] = rp.Hash
	d.roots[rp.Hash] = rp
	return
}

func (d *memoryDB) LastRoot(pk cipher.PubKey) (rp *RootPack, ok bool) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	roots := d.feeds[pk]
	if len(roots) == 0 {
		return // bo roots
	}
	var max uint64
	for seq := range roots {
		if max < seq {
			max = seq
		}
	}
	if rp, ok = d.roots[roots[max]]; !ok {
		panic("broken db: misisng root") // critical
	}
	return
}

func (d *memoryDB) RangeFeed(pk cipher.PubKey,
	fn func(rp *RootPack) (stop bool)) {

	d.mx.RLock()
	defer d.mx.RUnlock()

	roots := d.feeds[pk]
	if len(roots) == 0 {
		return // empty feed
	}

	var o = make(order, 0, len(roots))
	for seq := range roots {
		o = append(o, seq)
	}
	sort.Sort(o)

	var rp *RootPack
	var ok bool

	for _, seq := range o {
		if rp, ok = d.roots[roots[seq]]; !ok {
			panic("broken database: misisng root") // critical
		}
		if fn(rp) {
			return // break
		}
	}
}

func (d *memoryDB) RangeFeedReverse(pk cipher.PubKey,
	fn func(rp *RootPack) (stop bool)) {

	d.mx.RLock()
	defer d.mx.RUnlock()

	roots := d.feeds[pk]
	if len(roots) == 0 {
		return // empty feed
	}

	var o = make(order, 0, len(roots))
	for seq := range roots {
		o = append(o, seq)
	}
	sort.Sort(sort.Reverse(o))

	var rp *RootPack
	var ok bool

	for _, seq := range o {
		if rp, ok = d.roots[roots[seq]]; !ok {
			panic("broken database: misisng root") // critical
		}
		if fn(rp) {
			return // break
		}
	}
}

func (d *memoryDB) RangeFeedDelete(pk cipher.PubKey,
	fn func(rp *RootPack) (del bool)) {

	d.mx.Lock()
	defer d.mx.Unlock()

	roots := d.feeds[pk]
	if len(roots) == 0 {
		return // empty feed
	}

	var o = make(order, 0, len(roots))
	for seq := range roots {
		o = append(o, seq)
	}
	sort.Sort(o)

	// ordered
	for _, seq := range o {
		rp := d.roots[roots[seq]]
		if fn(rp) {
			delete(d.roots, roots[seq])
			delete(roots, seq)
		}
	}

}

//
// Roots
//

func (d *memoryDB) GetRoot(hash cipher.SHA256) (rp *RootPack, ok bool) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	rp, ok = d.roots[hash]
	return
}

func (d *memoryDB) DelRootsBefore(pk cipher.PubKey, seq uint64) {
	d.mx.Lock()
	defer d.mx.Unlock()

	roots := d.feeds[pk]
	if len(roots) == 0 {
		return // empty feed
	}

	var o = make(order, 0, len(roots))
	for s := range roots {
		if s < seq {
			o = append(o, s)
		}
	}
	if len(o) == 0 {
		return
	}
	sort.Sort(o)

	for _, s := range o {
		delete(d.roots, roots[s])
		delete(roots, s)
	}
}

func (d *memoryDB) Stat() (s Stat) {
	d.mx.RLock()
	defer d.mx.RUnlock()

	s.Objects = len(d.objects)
	for _, v := range d.objects {
		s.Space += Space(len(v))
	}

	if len(d.feeds) == 0 {
		return
	}
	s.Feeds = make(map[cipher.PubKey]FeedStat, len(d.feeds))
	// lengths of Prev, Hash, Sig and Seq (8 byte)
	add := len(cipher.SHA256{})*2 + len(cipher.Sig{}) + 8
	for pk, rs := range d.feeds {
		var fs FeedStat
		for _, hash := range rs {
			fs.Space += Space(len(d.roots[hash].Root) + add)
		}
		fs.Roots = len(rs)
		s.Feeds[pk] = fs
	}
	return
}

func (d *memoryDB) Close() (_ error) {
	return
}

type order []uint64

func (o order) Len() int           { return len(o) }
func (o order) Less(i, j int) bool { return o[i] < o[j] }
func (o order) Swap(i, j int)      { o[i], o[j] = o[j], o[i] }

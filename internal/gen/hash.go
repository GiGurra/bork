package gen

// hashRuntime hashes structure, independently of its displayed text.
// Container fallback supports types seen only through a generic parameter.
const hashRuntime = `package main

import (
 "hash/maphash"
 "reflect"
)

var _mapSeed = maphash.MakeSeed()

func _hashMix(a, b uint64) uint64 {
 return (a ^ (b + 0x9e3779b97f4a7c15 + (a << 6) + (a >> 2))) * 0xbf58476d1ce4e5b9
}

func _hash(x any) uint64 {
 if v, ok := x.(interface{ _borkHash() uint64 }); ok { return v._borkHash() }
 switch x := x.(type) {
 case nil: return 0
 case bool: return maphash.Comparable(_mapSeed, x)
 case string: return maphash.Comparable(_mapSeed, x)
 case int64: return maphash.Comparable(_mapSeed, x)
 case int32: return maphash.Comparable(_mapSeed, x)
 case int16: return maphash.Comparable(_mapSeed, x)
 case int8: return maphash.Comparable(_mapSeed, x)
 case uint64: return maphash.Comparable(_mapSeed, x)
 case uint32: return maphash.Comparable(_mapSeed, x)
 case uint16: return maphash.Comparable(_mapSeed, x)
 case uint8: return maphash.Comparable(_mapSeed, x)
 case float64: return maphash.Comparable(_mapSeed, x)
 case float32: return maphash.Comparable(_mapSeed, x)
 }
 return _hashValue(reflect.ValueOf(x))
}

func _hashOf[T comparable](x T) uint64 { return maphash.Comparable(_mapSeed, x) }

func _hashList[T any](xs []T, hash func(T) uint64) uint64 {
 var h uint64
 for _, x := range xs { h = _hashMix(h, hash(x)) }
 return h
}

func _hashValue(v reflect.Value) uint64 {
 if !v.IsValid() { return 0 }
 if v.CanInterface() {
  if x, ok := v.Interface().(interface{ _borkHash() uint64 }); ok { return x._borkHash() }
 }
 switch v.Kind() {
 case reflect.Interface:
  if v.IsNil() { return 0 }; return _hashValue(v.Elem())
 case reflect.Slice:
  var h uint64
  for i := 0; i < v.Len(); i++ { h = _hashMix(h, _hashValue(v.Index(i))) }
  return h
 case reflect.Struct:
  var h uint64
  for i := 0; i < v.NumField(); i++ { h = _hashMix(h, _hashValue(v.Field(i))) }
  return h
 case reflect.Bool: return _hash(v.Bool())
 case reflect.String: return _hash(v.String())
 case reflect.Int: return _hash(v.Int())
 case reflect.Int64: return _hash(v.Int())
 case reflect.Int32: return _hash(int32(v.Int()))
 case reflect.Int16: return _hash(int16(v.Int()))
 case reflect.Int8: return _hash(int8(v.Int()))
 case reflect.Uint: return _hash(v.Uint())
 case reflect.Uint64: return _hash(v.Uint())
 case reflect.Uint32: return _hash(uint32(v.Uint()))
 case reflect.Uint16: return _hash(uint16(v.Uint()))
 case reflect.Uint8: return _hash(uint8(v.Uint()))
 case reflect.Float64: return _hash(v.Float())
 case reflect.Float32: return _hash(float32(v.Float()))
 }
 panic("bork: cannot hash this value")
}
`

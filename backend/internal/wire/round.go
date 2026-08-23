// Package wire strips floating-point residue from figures on their way out
// of Stillhouse.
//
// The problem, filed as QA finding F17: a removal of 60 bottles at 40 % in
// 750 mL comes back as 18.000000000000004 LAA, and a duty of 0.84 as
// 0.8399999999999999. Neither is a measurement — both are the residue of
// binary floating-point arithmetic on figures that were exact decimals
// going in. The browser hid it behind display rounding, which meant the
// noise was invisible in the UI and fully present everywhere else: the
// MCP tools, curl against the ConnectRPC endpoints, and anything a
// licensee wrote against the API.
//
// The rule here is deliberately one rule rather than a table of
// per-field precisions. A table has to be kept in step with 140-odd
// proto fields and is wrong the first time somebody adds a field and
// forgets; and every coarser precision it could encode is a decision
// about *display*, which belongs at the display. Six decimal places is
// below anything a distillery measures — the finest figure in the
// system is an alcoholometric volume factor at five — and above every
// artefact of the arithmetic.
//
// It changes nothing stored and nothing computed. The value on the wire
// is the nearest float64 to the figure that was meant, which is what the
// caller was owed.
package wire

import (
	"math"
	"reflect"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Places is the precision every float on the wire is stated to.
const Places = 6

const scale = 1e6

// tooLargeToRound is where x*scale would start losing integer precision in
// a float64 (2^53 ≈ 9.007e15). No quantity in a distillery comes near it;
// the guard is here so that a figure that somehow does is passed through
// untouched rather than mangled.
const tooLargeToRound = 1e9

// Round states one figure to Places decimal places. NaN, infinities and
// implausibly large values pass through unchanged — rounding them would
// replace a visible problem with an invisible one.
func Round(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > tooLargeToRound {
		return x
	}
	return math.Round(x*scale) / scale
}

// Message rounds every double and float field in m, recursively, including
// those inside repeated fields and maps. Safe to call on a nil message.
func Message(m proto.Message) {
	if m == nil {
		return
	}
	r := m.ProtoReflect()
	if !r.IsValid() {
		return
	}
	roundMessage(r)
}

func roundMessage(m protoreflect.Message) {
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			roundMap(m, fd, v.Map())
		case fd.IsList():
			roundList(fd, v.List())
		case fd.Kind() == protoreflect.DoubleKind:
			m.Set(fd, protoreflect.ValueOfFloat64(Round(v.Float())))
		case fd.Kind() == protoreflect.FloatKind:
			m.Set(fd, protoreflect.ValueOfFloat32(float32(Round(v.Float()))))
		case fd.Kind() == protoreflect.MessageKind, fd.Kind() == protoreflect.GroupKind:
			roundMessage(v.Message())
		}
		return true
	})
}

func roundList(fd protoreflect.FieldDescriptor, list protoreflect.List) {
	for i := 0; i < list.Len(); i++ {
		v := list.Get(i)
		switch fd.Kind() {
		case protoreflect.DoubleKind:
			list.Set(i, protoreflect.ValueOfFloat64(Round(v.Float())))
		case protoreflect.FloatKind:
			list.Set(i, protoreflect.ValueOfFloat32(float32(Round(v.Float()))))
		case protoreflect.MessageKind, protoreflect.GroupKind:
			roundMessage(v.Message())
		}
	}
}

func roundMap(m protoreflect.Message, fd protoreflect.FieldDescriptor, mp protoreflect.Map) {
	val := fd.MapValue()
	mp.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		switch val.Kind() {
		case protoreflect.DoubleKind:
			mp.Set(k, protoreflect.ValueOfFloat64(Round(v.Float())))
		case protoreflect.FloatKind:
			mp.Set(k, protoreflect.ValueOfFloat32(float32(Round(v.Float()))))
		case protoreflect.MessageKind, protoreflect.GroupKind:
			roundMessage(v.Message())
		}
		return true
	})
	_ = m
}

// Struct rounds every float64 and float32 reachable from v, which must be a
// pointer to a struct (or a struct passed by value, in which case a rounded
// copy is returned). It exists for the MCP tools that assemble a plain Go
// struct from several proto responses rather than returning one of them —
// those never pass through Message, and were the last place the residue
// still reached a caller.
func Struct(v any) any {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return v
	}
	if rv.Kind() != reflect.Pointer {
		// Take an addressable copy so the walk below can write.
		copyPtr := reflect.New(rv.Type())
		copyPtr.Elem().Set(rv)
		roundValue(copyPtr.Elem())
		return copyPtr.Elem().Interface()
	}
	if rv.IsNil() {
		return v
	}
	roundValue(rv.Elem())
	return v
}

func roundValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Float64, reflect.Float32:
		if v.CanSet() {
			v.SetFloat(Round(v.Float()))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			roundValue(v.Elem())
		}
	case reflect.Struct:
		// Skip anything from the protobuf runtime: its internal state is
		// not ours to walk, and a proto message reached this way should
		// go through Message instead.
		if strings.HasPrefix(v.Type().PkgPath(), "google.golang.org/protobuf") {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				roundValue(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			roundValue(v.Index(i))
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			if mv.Kind() == reflect.Float64 || mv.Kind() == reflect.Float32 {
				v.SetMapIndex(k, reflect.ValueOf(Round(mv.Float())).Convert(mv.Type()))
				continue
			}
			// Map values are not addressable; round a copy and put it back.
			cp := reflect.New(mv.Type()).Elem()
			cp.Set(mv)
			roundValue(cp)
			v.SetMapIndex(k, cp)
		}
	}
}

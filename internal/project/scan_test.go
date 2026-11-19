// SPDX-License-Identifier: Apache-2.0

package project

import (
	"reflect"
	"testing"
)

func TestTopLevelNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		src  string
		want []string
	}{
		{"package", "a.sysml", "package Vehicle { part def Wheel; }", []string{"Vehicle"}},
		{"short name and name", "a.sysml", "package <v> Vehicle;", []string{"v", "Vehicle"}},
		{"short name only", "a.sysml", "package <v> { }", []string{"v"}},
		{"quoted names", "a.sysml", "package <'1.2'> 'Odd Name' { }", []string{"1.2", "Odd Name"}},
		{"escapes in a quoted name", "a.sysml", `package 'it\'s \\ here';`, []string{`it's \ here`}},
		{"library package", "a.kerml", "standard library package <B> Base { }", []string{"B", "Base"}},
		{"private member", "a.sysml", "private package Hidden;", []string{"Hidden"}},
		{"kerml namespace", "a.kerml", "namespace <n> N { class Inner; }", []string{"n", "N"}},
		{"kerml types and features", "a.kerml", "abstract class <c> C specializes Base::Anything { feature x; }\ndatatype D;\nfeature f : C;\nclass all E;", []string{"c", "C", "D", "f", "E"}},
		{"sysml definitions", "a.sysml", "part def <p> Part :> Base { part inner; }\nabstract use case def 'Use It';\nenum def Colour { red; }", []string{"p", "Part", "Use It", "Colour"}},
		{"sysml usages", "a.sysml", "part wheel : Wheel[4];\nref item <i> thing;\nx : Real = 1;", []string{"wheel", "i", "thing", "x"}},
		{"prefix metadata", "a.sysml", "#Safety::critical part def Brake;\n#fmea def Mode;", []string{"Brake", "Mode"}},
		{"two declarations on one line", "a.sysml", "package A; package B { } package C;", []string{"A", "B", "C"}},
		{"declaration after a closing brace", "a.sysml", "package A {\n  part def X;\n} package B;", []string{"A", "B"}},
		{"declaration split over lines", "a.sysml", "package\n  <a>\n  A\n{\n}", []string{"a", "A"}},
		{"braces in comments and strings", "a.sysml", "package A {\n  doc /* } */\n  // }\n  //* } */\n  attribute s = \"}\";\n  part def 'odd } name';\n  package Nested;\n}\npackage B;", []string{"A", "B"}},
		{"declarations in comments and strings", "a.sysml", "// package Note;\n//* package Long;\n */\n/* package Body; */\ndoc /* package Doc; */\nattribute s = \"; package Str;\";\npackage Real;", []string{"s", "Real"}},
		{"unterminated comment", "a.sysml", "package A;\n/* package B;", []string{"A"}},
		{"a name is not a keyword of the other language", "a.sysml", "part def class;\nattribute feature;", []string{"class", "feature"}},
		{"a keyword only SysML 2.1 reserves", "a.sysml", "part def new;", []string{"new"}},
		{"keyword use is not a name", "a.kerml", "class part;\nfeature def;", []string{"part", "def"}},
		{"imports, aliases and filters", "a.sysml", "private import Base::*;\npublic import all Other::Thing;\nalias <s> Short for Base::Long;\nfilter @Safety;", nil},
		{"anonymous elements", "a.sysml", "package;\npart : Wheel;\nattribute :>> mass = 1;\ndoc /* text */\ncomment about A /* text */\npart redefines wheel;", nil},
		{"named annotations", "a.sysml", "comment <c> Note about A /* text */\ndoc Text /* text */\nrep Code language \"C\" /* text */", []string{"c", "Note", "Text", "Code"}},
		{"references are not declarations", "a.sysml", "dependency A to B;\ndependency A, C to B;\ndependency Use from A to B;\nconnect a to b;\nflow a.x to b.y;\nflow c to d;\nflow f from a.x to b.y;\nmetadata Approved about A;\n@Approved;\nmetadata m : Approved;\n@n : Approved;\nperform start;\nperform action go;\nassert not satisfy R by a;\nsatisfy requirement r : R by a;", []string{"Use", "f", "m", "n", "go", "r"}},
		{"kerml relationships", "a.kerml", "specialization S subtype A specializes B;\nsubtype A specializes B;\nconnector a to b;\nconnector c from a to b;\nbinding a = b;\nbinding e of a = b;\nsuccession a then b;\nsuccession s first a then b;\nfeaturing f by T;\nfeaturing G of f by T;\ndisjoining D disjoint A from B;", []string{"S", "c", "e", "s", "G", "D"}},
		{"body expression", "a.kerml", "feature n = list->collect { in x; x + 1 };\nfeature m = (a < b) && (c > d);\nclass Z;", []string{"n", "m", "Z"}},
		{"not a textual file", "a.json", "package A;", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := topLevelNames(tc.file, []byte(tc.src)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s\n got  %q\n want %q", tc.src, got, tc.want)
			}
		})
	}
}

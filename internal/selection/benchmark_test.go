package selection

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

func BenchmarkEvaluateSearch10000(b *testing.B) {
	input := make([]InputComment, 0, 20000)
	for i := 0; i < 10000; i++ {
		for j := 0; j < 2; j++ {
			input = append(input, InputComment{ID: fmt.Sprintf("comment-%d-%d", i, j), Nickname: fmt.Sprintf("사용자%d", i), Identifier: fmt.Sprintf("uid%d", i), ParticipantKind: Fixed, Kind: Text, Text: "응모합니다 당첨 참가"})
		}
	}
	participants, err := BuildParticipants(input)
	if err != nil {
		b.Fatal(err)
	}
	filter := Filters{IncludeKeywords: []string{"응모"}, ExcludeKeywords: []string{"탈락"}}
	samples := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		started := time.Now()
		result, err := Evaluate(participants, filter, nil)
		if err != nil {
			b.Fatal(err)
		}
		rows := Search(result.Rows, "사용자1")
		if result.Included != 10000 || len(rows) == 0 {
			b.Fatal("benchmark invariant")
		}
		samples = append(samples, time.Since(started))
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		b.ReportMetric(float64(samples[(len(samples)*95+99)/100-1].Nanoseconds())/1e6, "p95-ms")
	}
}

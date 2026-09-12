package service

func cacheHitTokens(u *parsedUsage) int {
	if u == nil {
		return 0
	}
	if u.CacheHitTokens > 0 {
		return u.CacheHitTokens
	}
	if u.CachedTokens > 0 {
		return u.CachedTokens
	}
	return u.CacheReadInputTokens
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func attachChatCacheUsage(usage map[string]any, u *parsedUsage) {
	if usage == nil {
		return
	}
	hit := cacheHitTokens(u)
	miss := 0
	write := 0
	think := 0
	if u != nil {
		miss = u.CacheMissTokens
		write = firstPositive(u.CacheWriteTokens, u.CacheCreationInputTokens)
		think = u.ThinkingTokens
	}
	usage["prompt_cache_hit_tokens"] = hit
	usage["prompt_cache_miss_tokens"] = miss
	usage["prompt_cache_write_tokens"] = write
	usage["cache_read_input_tokens"] = firstPositive(uNilInt(u, func(p *parsedUsage) int { return p.CacheReadInputTokens }), hit)
	usage["cache_creation_input_tokens"] = firstPositive(uNilInt(u, func(p *parsedUsage) int { return p.CacheCreationInputTokens }), write)
	usage["cached_tokens"] = hit
	usage["prompt_tokens_details"] = map[string]any{"cached_tokens": hit}
	if think > 0 {
		usage["completion_thinking_tokens"] = think
		usage["completion_tokens_details"] = map[string]any{"reasoning_tokens": think}
	}
}

func attachResponsesCacheUsage(usage map[string]any, u *parsedUsage) {
	if usage == nil {
		return
	}
	hit := cacheHitTokens(u)
	miss := 0
	think := 0
	if u != nil {
		miss = u.CacheMissTokens
		think = u.ThinkingTokens
	}
	usage["input_tokens_details"] = map[string]any{"cached_tokens": hit}
	usage["prompt_cache_hit_tokens"] = hit
	usage["prompt_cache_miss_tokens"] = miss
	if think > 0 {
		usage["output_tokens_details"] = map[string]any{"reasoning_tokens": think}
	}
}

func attachAnthropicCacheUsage(usage map[string]any, u *parsedUsage) {
	if usage == nil {
		return
	}
	hit := cacheHitTokens(u)
	write := 0
	if u != nil {
		write = firstPositive(u.CacheCreationInputTokens, u.CacheWriteTokens)
	}
	usage["cache_read_input_tokens"] = hit
	usage["cache_creation_input_tokens"] = write
}

func uNilInt(u *parsedUsage, fn func(*parsedUsage) int) int {
	if u == nil {
		return 0
	}
	return fn(u)
}

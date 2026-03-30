---
authors:
- mike
tags:
- continuous delivery
- agentic coding
- llms
date: 2026-03-30
---

![feedback loops meme|wide](./feedback-loops.jpg)

# Feedback Loops

## Story Time

I was working one day and came across an issue, and of course, it involved Kubernetes. The problem was configuration really. I was installing a common service lots of people use in Kubernetes environments, nothing exotic. To avoid causing small outages during upgrades we had decided to run it in a highly available manner. Due to reasons related to open source and rug pulling, the HA installation method was slightly broken when I started the work with the task. I actually got it working at first, but then I had to do some things like, actually make it secure, and that is when it broke.

I was diagnosing it first myself and fixed couple fairly obvious issues, but still no dice. Although my focus was also partially in other things, this probably took couple hours all in all, so not exactly fast progress. That's when I thought why am I fixing this myself? Let an agent handle it.

Problem was that I was working in a setting where I cannot let a coding agent like Claude Code touch the actual cluster I'm targeting. Instead, I setup a local Kind cluster on another machine and got the latest configuration there for an installation. I told Claude Code to bring up the service and fix the issues I was observing. 10 to 20 minutes later the issue was solved while my focus on was on other urgent matters.

Now if you are familiar with fixing issues using chat based LLMs, like copy-pasting between ChatGPT in your browser and your IDE/text editor. You might think I still have to test the fix. But if you were reading closely you would know that I setup a cluster, and I had Claude Code working with a real cluster (well, a Kind cluster, but good enough). I told Claude to verify the work and not bother me until it was done with this. Typically with application code Claude Code will not ask you stupid questions and works in a closed loop until done, but with work that involves infrastructure I find I will have to prompt it often to really "close the loop" and verify the changes.

And sure enough, with this **feedback loop** Claude Code fixed the issues rather quickly and verified it works. When I moved the code to the real environment, it still worked.

## So about those loops

Well, this wasn't really a novel observation that we need a feedback loop. I've seen it mentioned over and over again lately. For example the [Zen of AI Coding](https://nonstructured.com/zen-of-ai-coding/) mentions tight feedback loops as one of the 16 *things*. What really amazes me though is that feedback loops is really the core thing in DevOps. I'm sure you have seen the "DevOps infinity loop" more than a handful of times if you have paid any attention to the DevOps movement in past 10+ years.

For over a decade we have known that tight feedback loops are important and still the Zen of AI Coding must include that? I found it a bit funny. Of course I have a horse in the race, because I've defined DevOps in terms of feedback for a long time. To me that is really the impact of DevOps done right. The shared understanding and the sense of ownership and accomplishment when building software, amongst other things of course.

With LLMs we need these loops more than ever, because they can remove so much more toil than previously possible. That is exciting to me; removing toil. Like this problem with installing a common service, I don't want to deal with that in future. I would have lots more to say about this topic, but let's leave it for future posts.

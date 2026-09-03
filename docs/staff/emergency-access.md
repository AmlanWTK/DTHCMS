# Emergency access, and the audit trail

Instructions for DTHC staff. English first, বাংলা below. The first part is for any
clinician; the second is for the administrator who watches the trail.

Technical reference: [`../audit.md`](../audit.md).

---

## English

### When you need a record your role does not reach

There is a door for it. It is meant for the patient in front of you who cannot wait — the
unconscious person whose regular physician is unreachable — and it is loud on purpose:
every administrator is told the moment you open it, your reason is kept word for word,
and everything you read under it is on the record with your name.

1. Open **Clinical → Emergency access**.
2. Type the **patient id** (from the wristband or the registration desk), or choose
   _Something else_ and say in words what you need.
3. Write **why**, in your own words, at least twenty characters. "Urgent" is not a reason;
   "Unconscious patient in room 2; regular physician unreachable" is.
4. Choose **for how long** — four hours unless you know better.
5. Press **Break the glass** and confirm with the code from your authenticator app.

The access shows under _Your open accesses_ with when it ends and whether an administrator
has seen it yet. **Close** it when you are done; it closes itself at the time you chose.

Do not use it for convenience, and never for a colleague — the record says who opened it,
and the reason is read by a person.

### For the administrator: the alarm

When somebody breaks the glass, a red banner appears at the top of every page of your
console within half a minute, in your language, saying who, for what, until when, and why.
Read it. If it makes sense, press **I have seen this**; that is recorded too. If it does
not, open the audit trail, and if necessary the account page: you can close the access
from the trail's break-glass list, suspend the account, or sign the person out everywhere.

The same banner appears if the audit chain ever fails verification. That is not a
clinical emergency but it is a serious one: tell the developer the same day.

### Reading the trail

**Administration → Audit trail** lists everything the system recorded about itself: every
sign-in and refusal, every role given or taken, every password or authenticator reset,
every export, every break-glass. Each line is a sentence — "10:42 — A001 granted
NUTRITIONIST to N006" — in the language you are using. Narrow it by a person's employee
code, a kind of event, a day, or a patient.

**Verify the chain** recomputes every entry against the one before it and tells you the
trail is intact, or names the first entry that is not. Run it when you are asked whether
the record can be trusted, and whenever you are about to export.

### Exporting a signed copy

**Export signed PDF** saves two files: the trail you narrowed to, and a small signature
file beside it. Keep the two together — either alone proves nothing. Anyone given both,
plus the clinic's public key (ask the developer; it is printed in the operations guide),
can check that the PDF is exactly what the system produced, without access to the system.
The export itself appears in the trail, with how many entries left.

The PDF is in English. The Bengali sentences are in the viewer; a Bengali PDF is on the
list.

---

## বাংলা

### যখন এমন রেকর্ড দরকার যেখানে আপনার ভূমিকার প্রবেশাধিকার নেই

এর জন্য একটি দরজা আছে। এটি সামনের সেই রোগীর জন্য যিনি অপেক্ষা করতে পারেন না — অজ্ঞান
রোগী, যাঁর নিয়মিত চিকিৎসককে পাওয়া যাচ্ছে না — আর ইচ্ছাকৃতভাবেই এটি সশব্দ: খোলার সঙ্গে সঙ্গে
সব প্রশাসক জানতে পারেন, আপনার কারণ হুবহু সংরক্ষিত থাকে, আর এর অধীনে আপনি যা পড়েন সবই
আপনার নামে রেকর্ডে থাকে।

1. **ক্লিনিক্যাল → জরুরি প্রবেশাধিকার** খুলুন।
2. **রোগীর আইডি** লিখুন (রিস্টব্যান্ড বা নিবন্ধন ডেস্ক থেকে), অথবা _অন্য কিছু_ বেছে নিয়ে
   লিখুন কী দরকার।
3. **কেন**, নিজের ভাষায়, কমপক্ষে বিশ অক্ষরে লিখুন। "জরুরি" কোনো কারণ নয়; "২ নম্বর ঘরে
   অজ্ঞান রোগী; নিয়মিত চিকিৎসককে পাওয়া যাচ্ছে না" — এটি কারণ।
4. **কতক্ষণের জন্য** বেছে নিন — অন্য কিছু জানা না থাকলে চার ঘণ্টা।
5. **জরুরি প্রবেশাধিকার নিন** চেপে অথেনটিকেটর অ্যাপের কোড দিয়ে নিশ্চিত করুন।

প্রবেশাধিকারটি _আপনার খোলা প্রবেশাধিকার_-এর নিচে দেখা যাবে — কখন শেষ হবে, আর কোনো প্রশাসক
এখনো দেখেছেন কি না। কাজ শেষ হলে **বন্ধ করুন**; আপনার বেছে নেওয়া সময়ে এটি নিজে থেকেই বন্ধ
হয়ে যাবে।

সুবিধার জন্য এটি ব্যবহার করবেন না, আর কখনোই সহকর্মীর জন্য নয় — রেকর্ডে থাকে কে খুলেছেন, আর
কারণটি একজন মানুষ পড়েন।

### প্রশাসকের জন্য: সতর্কবার্তা

কেউ জরুরি প্রবেশাধিকার নিলে আধা মিনিটের মধ্যে আপনার কনসোলের প্রতিটি পাতার উপরে একটি লাল
ব্যানার আসবে, আপনার ভাষায় — কে, কীসের জন্য, কতক্ষণ পর্যন্ত, আর কেন। পড়ুন। যুক্তিসঙ্গত মনে
হলে **আমি দেখেছি** চাপুন; সেটিও রেকর্ডে থাকে। না হলে অডিট ট্রেইল খুলুন, দরকার হলে অ্যাকাউন্ট
পাতাও: ট্রেইলের জরুরি প্রবেশাধিকারের তালিকা থেকে প্রবেশাধিকার বন্ধ করতে পারেন, অ্যাকাউন্ট
স্থগিত করতে পারেন, বা সব জায়গা থেকে সাইন আউট করাতে পারেন।

অডিট চেইন কখনো যাচাইয়ে ব্যর্থ হলেও একই ব্যানার আসবে। সেটি ক্লিনিক্যাল জরুরি অবস্থা নয়, তবে
গুরুতর: সেদিনই ডেভেলপারকে জানান।

### ট্রেইল পড়া

**প্রশাসন → অডিট ট্রেইল**-এ সিস্টেম নিজের সম্পর্কে যা রেকর্ড করেছে সব থাকে: প্রতিটি সাইন ইন ও
প্রত্যাখ্যান, প্রতিটি ভূমিকা দেওয়া বা নেওয়া, প্রতিটি পাসওয়ার্ড বা অথেনটিকেটর রিসেট, প্রতিটি
রপ্তানি, প্রতিটি জরুরি প্রবেশাধিকার। প্রতিটি লাইন একটি বাক্য — "১০:৪২ — A001 N006-কে
NUTRITIONIST ভূমিকা দিয়েছেন" — আপনার ব্যবহৃত ভাষায়। কর্মী কোড, ঘটনার ধরন, দিন বা রোগী দিয়ে
সংকুচিত করুন।

**চেইন যাচাই করুন** প্রতিটি এন্ট্রিকে তার আগেরটির সাপেক্ষে পুনর্গণনা করে জানায় ট্রেইল অক্ষত
আছে, নাকি কোন এন্ট্রিতে প্রথম গরমিল। রেকর্ড বিশ্বাসযোগ্য কি না জিজ্ঞাসা করা হলে, আর রপ্তানির
আগে, এটি চালান।

### স্বাক্ষরিত কপি রপ্তানি

**স্বাক্ষরিত PDF রপ্তানি** দুটি ফাইল সংরক্ষণ করে: আপনার সংকুচিত ট্রেইল, আর তার পাশে একটি ছোট
স্বাক্ষর ফাইল। দুটি একসঙ্গে রাখুন — একটি একা কিছু প্রমাণ করে না। এই দুটি আর ক্লিনিকের পাবলিক
কী (ডেভেলপারকে জিজ্ঞাসা করুন; অপারেশন গাইডে ছাপা আছে) থাকলে যে-কেউ সিস্টেমে প্রবেশ না করেই
যাচাই করতে পারবেন PDF-টি ঠিক সিস্টেমেরই তৈরি। রপ্তানিটিও ট্রেইলে থাকে, কতটি এন্ট্রি বেরিয়েছে
তা-সহ।

PDF-টি ইংরেজিতে। বাংলা বাক্যগুলো ভিউয়ারে আছে; বাংলা PDF তালিকায় আছে।

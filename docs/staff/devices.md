# Clinic tablets and phones

Instructions for DTHC staff. English first, বাংলা below. For the administrator who sets a
device up, and for anyone who finds a tablet saying it is not enrolled.

Technical reference: [`../identity.md`](../identity.md) §9.

---

## English

### Why a tablet has to be enrolled

Every value entered into DTHCMS is recorded with who entered it, from which device, and
when. For "which device" to mean anything, the tablet has to be one the clinic knows — so
each one is **enrolled** once by an administrator, and from then on it proves itself on
every request. A tablet that is not enrolled can still sign a person in, but it cannot
record clinical values. It says so on its sign-in screen.

### Enrolling a new tablet (administrator, five minutes)

1. In the console, open **Administration → Devices**.
2. Under _Register a device_, give it a name the clinic will use — "Anthropometry
   tablet 2", "Field phone (Rahim)" — pick tablet or phone, and press **Register and get a
   code**.
3. A code appears, `XXXXX-XXXXX`. It works for **fifteen minutes** and is shown only once.
4. On the tablet, open the DTHCMS app. On the sign-in screen the bottom line says _This
   device: not enrolled_. Press **Device**.
5. Type the code and press **Enrol this device**. Case, spaces and dashes do not matter,
   and a zero or a one is read as the letter it looks like.
6. The screen says _Enrolled as (name)_. Done. Back in the console, the device shows as
   **Active**, with its model and the app version, and _Last seen_ updates every time it
   talks to the server.

If the code expires or is lost, press **New code** on that device's row and start from
step 4. The old code stops working the moment a new one is issued.

### When a tablet is reinstalled, reset, or has its screen lock removed

It loses its enrolment — that is by design; the key that proved it was the tablet was
wiped with everything else. The sign-in screen will say _not enrolled_ again. An
administrator presses **New code** on that device's row and the tablet is enrolled with
the same name, the same history, and a fresh key. Nothing needs deleting.

### When a tablet goes missing

Do not wait to be sure. In **Administration → Devices**:

- **Suspend** — it might turn up. The tablet is refused until you **Reinstate** it, and
  anyone signed in on it is signed out on their next request. A suspension can be undone.
- **Report lost** — it is gone. This cannot be undone. Anything the tablet had saved while
  offline and sends later is held for review rather than accepted, in case the person
  who had it was entering real values that morning.
- **Revoke** — it is retired for good (cracked screen, returned to the supplier). This
  cannot be undone either. To use the hardware again later, register it as a new device.

Every one of these asks for a reason. Write a real one; it is what the next person reads.

### Please

- Enrol a tablet only if it belongs to the clinic. A personal phone that is enrolled can
  record clinical values; think before you do that.
- Never read an enrolment code out over the phone or send it in a message. Walk it over.
- If a tablet says _not enrolled_ and you did not expect that, tell an administrator the
  same day. It may have been reset — or revoked, and somebody should know why.

---

## বাংলা

### ট্যাবলেট নিবন্ধন করতে হয় কেন

DTHCMS-এ লেখা প্রতিটি মান নথিভুক্ত হয় কে লিখেছেন, কোন ডিভাইস থেকে, আর কখন — এই তিন তথ্যসহ।
"কোন ডিভাইস" কথাটার অর্থ থাকতে হলে ট্যাবলেটটি ক্লিনিকের পরিচিত হতে হবে। তাই প্রতিটি ট্যাবলেট
একজন অ্যাডমিনিস্ট্রেটর একবার **নিবন্ধন** করেন, আর এরপর প্রতিটি অনুরোধে ট্যাবলেটটি নিজেই নিজের
পরিচয় প্রমাণ করে। নিবন্ধন না করা ট্যাবলেট থেকে সাইন ইন করা যায়, কিন্তু ক্লিনিক্যাল মান লেখা
যায় না। সাইন ইন স্ক্রিনেই সেটি লেখা থাকে।

### নতুন ট্যাবলেট নিবন্ধন (অ্যাডমিনিস্ট্রেটর, পাঁচ মিনিট)

১. কনসোলে **অ্যাডমিনিস্ট্রেশন → ডিভাইস** খুলুন।
২. _ডিভাইস নিবন্ধন করুন_-এর নিচে এমন একটি নাম দিন যা ক্লিনিকে সবাই ব্যবহার করবে — "দেহমাপ
ট্যাবলেট ২", "মাঠের ফোন (রহিম)" — ট্যাবলেট না ফোন বেছে নিন, তারপর **নিবন্ধন করে কোড নিন**
চাপুন।
৩. একটি কোড দেখা যাবে, `XXXXX-XXXXX`। এটি **পনেরো মিনিট** কাজ করে এবং একবারই দেখানো হয়।
৪. ট্যাবলেটে DTHCMS অ্যাপ খুলুন। সাইন ইন স্ক্রিনের নিচের লাইনে লেখা থাকবে _এই ডিভাইস: নিবন্ধিত
নয়_। **ডিভাইস** চাপুন।
৫. কোডটি লিখে **এই ডিভাইস নিবন্ধন করুন** চাপুন। বড়-ছোট হাতের অক্ষর, ফাঁকা জায়গা বা ড্যাশে
কিছু যায় আসে না; শূন্য বা এক লিখলে যে অক্ষরের মতো দেখায় সেটিই ধরা হয়।
৬. স্ক্রিনে লেখা আসবে _(নাম) হিসেবে নিবন্ধিত_। হয়ে গেল। কনসোলে ডিভাইসটি এখন **সক্রিয়**
দেখাবে, মডেল ও অ্যাপের সংস্করণসহ, আর সার্ভারের সাথে যতবার কথা বলবে _শেষ দেখা_ ততবার বদলাবে।

কোডের মেয়াদ শেষ হলে বা হারিয়ে গেলে সেই ডিভাইসের সারিতে **নতুন কোড** চেপে ৪ নম্বর ধাপ থেকে
আবার শুরু করুন। নতুন কোড দেওয়ার সাথে সাথে পুরোনোটি অচল হয়ে যায়।

### ট্যাবলেট রিইনস্টল, রিসেট বা স্ক্রিন লক খুলে ফেলা হলে

নিবন্ধন হারিয়ে যায় — এটাই নিয়ম; ট্যাবলেটের পরিচয় প্রমাণ করত যে কী, সেটিও বাকি সবকিছুর সাথে
মুছে গেছে। সাইন ইন স্ক্রিন আবার _নিবন্ধিত নয়_ দেখাবে। অ্যাডমিনিস্ট্রেটর সেই ডিভাইসের সারিতে
**নতুন কোড** চাপলে ট্যাবলেটটি একই নাম, একই ইতিহাস আর নতুন কী নিয়ে আবার নিবন্ধিত হয়। কিছু
মুছতে হয় না।

### ট্যাবলেট হারিয়ে গেলে

নিশ্চিত হওয়ার অপেক্ষা করবেন না। **অ্যাডমিনিস্ট্রেশন → ডিভাইস**-এ:

- **স্থগিত করুন** — হয়তো পাওয়া যাবে। **পুনরায় চালু** না করা পর্যন্ত ট্যাবলেটটি প্রত্যাখ্যাত হবে,
  আর এতে সাইন ইন থাকা যে কেউ পরের অনুরোধেই সাইন আউট হয়ে যাবেন। স্থগিত করা ফেরানো যায়।
- **হারানো ঘোষণা করুন** — এটি আর নেই। এটি ফেরানো যায় না। অফলাইনে ট্যাবলেটে জমে থাকা কিছু পরে
  এসে পৌঁছালে তা গ্রহণ না করে পর্যালোচনার জন্য আটকে রাখা হয় — কারণ যাঁর হাতে ছিল তিনি হয়তো
  সেদিন সকালে সত্যিকারের মান লিখছিলেন।
- **বাতিল করুন** — এটি চিরতরে অবসরে গেল (স্ক্রিন ফাটা, সরবরাহকারীকে ফেরত)। এটিও ফেরানো যায়
  না। হার্ডওয়্যারটি পরে আবার ব্যবহার করতে হলে নতুন ডিভাইস হিসেবে নিবন্ধন করুন।

প্রতিটিতেই একটি কারণ চাওয়া হয়। সত্যিকারের কারণ লিখুন; পরের জন যেটি পড়বেন সেটিই এটি।

### অনুরোধ

- শুধু ক্লিনিকের ট্যাবলেটই নিবন্ধন করুন। নিবন্ধিত ব্যক্তিগত ফোন থেকে ক্লিনিক্যাল মান লেখা যায়;
  করার আগে ভাবুন।
- নিবন্ধন কোড কখনো ফোনে বলবেন না বা মেসেজে পাঠাবেন না। হেঁটে গিয়ে দিন।
- কোনো ট্যাবলেট _নিবন্ধিত নয়_ দেখালে এবং আপনি তা আশা না করে থাকলে সেদিনই অ্যাডমিনিস্ট্রেটরকে
  জানান। হয়তো রিসেট হয়েছে — অথবা বাতিল করা হয়েছে, আর কেন, সেটা কারও জানা দরকার।
